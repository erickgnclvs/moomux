package ipc

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// proxyOn serves ServeProxy on a loopback port and returns its URL.
func proxyOn(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() { _ = ServeProxy(ln) }()
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// getVia fetches target through the proxy at proxy, trusting srv's cert.
func getVia(t *testing.T, proxy *url.URL, srv *httptest.Server) string {
	t.Helper()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(proxy)
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func helloServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from the mac")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A TLS request through the proxy is a CONNECT tunnel end to end, the path
// WebKit takes for every page.
func TestServeProxyTunnelsConnect(t *testing.T) {
	if got := getVia(t, proxyOn(t), helloServer(t)); got != "hello from the mac" {
		t.Fatalf("body = %q", got)
	}
}

// A dev server that isn't running is a 502 naming the dial error, which is
// what the phone's browser shows. Port 0 can never be dialled, so nothing
// can be listening there by the time the proxy tries.
func TestServeProxyTargetDown(t *testing.T) {
	tr := &http.Transport{Proxy: http.ProxyURL(proxyOn(t))}
	defer tr.CloseIdleConnections()
	_, err := (&http.Client{Transport: tr}).Get("https://127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("err = %v, want a 502 from the proxy", err)
	}
}

// A client that half-closes after its request still gets the answer: the
// tunnel stays open in the direction that hasn't finished.
func TestServeProxyKeepsTheReplyAfterAHalfClose(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		got, _ := io.ReadAll(c) // answers only once the client is done
		_, _ = c.Write(append([]byte("echo "), got...))
	}()

	c, err := net.Dial("tcp", proxyOn(t).Host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "CONNECT "+target.Addr().String()+" HTTP/1.1\r\nHost: x\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	_, _ = io.WriteString(c, "ping")
	_ = c.(*net.TCPConn).CloseWrite()
	if got, _ := io.ReadAll(br); string(got) != "echo ping" {
		t.Fatalf("reply = %q, want %q", got, "echo ping")
	}
}

// Start brings the proxy up beside the listener, behind the same check and
// the same cache: a phone that uses both ports costs one whois, not two.
func TestTailnetServesTheProxyBesideTheListener(t *testing.T) {
	var whois atomic.Int32
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) { whois.Add(1); return "me@example.com", nil }
	defer func() { tailscaleLoginFn = restore }()

	// A freed port can be retaken before the proxy binds it: vanishingly
	// rare, and it fails loudly rather than passing wrongly.
	spare, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := spare.Addr().(*net.TCPAddr).Port
	spare.Close()

	tn := &Tailnet{Srv: &Server{}, ProxyPort: port, listen: boundOn(t, "127.0.0.1", "me@example.com")}
	tn.Start()
	<-tn.bound

	if _, err := (&Client{Socket: tn.ln.Addr().String()}).Themes(); err != nil {
		t.Fatalf("IPC call: %v", err)
	}
	proxy := &url.URL{Scheme: "http", Host: spare.Addr().String()}
	if got := getVia(t, proxy, helloServer(t)); got != "hello from the mac" {
		t.Fatalf("body = %q", got)
	}
	if n := whois.Load(); n != 1 {
		t.Fatalf("whois ran %d times, want 1 shared by both ports", n)
	}
}
