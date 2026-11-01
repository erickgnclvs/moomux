package ipc

import (
	"bufio"
	"errors"
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

// connectVia sends a CONNECT for target to the proxy at proxyAddr and
// returns the open conn and the proxy's status.
func connectVia(t *testing.T, proxyAddr, target string) (net.Conn, int) {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: x\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	return c, resp.StatusCode
}

// Only a name nobody can register may resolve inward: anything else doing
// so is DNS rebinding. And moomux's own ports are never a target.
func TestProxyAllowTarget(t *testing.T) {
	p := &proxy{self: "100.71.5.0:45877"}
	for _, tc := range []struct {
		name, addr string
		ok         bool
	}{
		{"127.0.0.1", "127.0.0.1:9000", true},
		{"::1", "[::1]:9000", true},
		{"localhost", "127.0.0.1:3000", true},
		{"LocalHost.", "[::1]:3000", true},
		{"shop.dev.localhost", "127.0.0.1:9000", true},
		{"shop.dev.invalid", "127.0.0.1:9000", true},
		{"192.168.1.1", "192.168.1.1:80", true},
		{"example.com", "93.184.216.34:443", true},
		{"rebind.example", "127.0.0.1:9000", false},
		{"rebind.example", "[::ffff:127.0.0.1]:9000", false},
		{"rebind.example", "10.0.0.5:80", false},
		{"rebind.example", "169.254.169.254:80", false},
		{"rebind.example", "100.100.1.1:22", false},
		{"localhost.example", "127.0.0.1:9000", false},
		{"100.71.5.0", "100.71.5.0:45877", false}, // the proxy itself
		{"100.71.5.0", "100.71.5.0:45876", false}, // the IPC listener
		{"100.71.5.0", "100.71.5.0:22", true},
	} {
		err := p.allowTarget(tc.name, tc.addr)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, errRefusedTarget)) {
			t.Errorf("allowTarget(%q, %q) = %v, want ok %v", tc.name, tc.addr, err, tc.ok)
		}
	}
}

// A refusal is a 403, distinct from a server that isn't up.
func TestServeProxyRefusesItself(t *testing.T) {
	u := proxyOn(t)
	if _, code := connectVia(t, u.Host, u.Host); code != http.StatusForbidden {
		t.Fatalf("CONNECT to the proxy itself: %d, want 403", code)
	}
}

func TestServeProxyCapsTunnels(t *testing.T) {
	srv := httptest.NewServer(&proxy{slots: make(chan struct{})}) // no free slot
	defer srv.Close()
	if _, code := connectVia(t, srv.Listener.Addr().String(), "127.0.0.1:1"); code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", code)
	}
}

// A tunnel nothing crosses is closed, freeing its slot and descriptors.
func TestServeProxyClosesIdleTunnels(t *testing.T) {
	srv := httptest.NewServer(&proxy{slots: make(chan struct{}, 1), idle: 50 * time.Millisecond})
	defer srv.Close()
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		c, err := target.Accept()
		if err == nil {
			defer c.Close()
			_, _ = io.Copy(io.Discard, c) // silent, and never hangs up
		}
	}()
	c, code := connectVia(t, srv.Listener.Addr().String(), target.Addr().String())
	if code != http.StatusOK {
		t.Fatalf("CONNECT: %d", code)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read on an idle tunnel: %v, want EOF from the proxy closing it", err)
	}
}
