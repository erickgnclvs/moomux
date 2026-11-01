package ipc

import (
	"io"
	"net"
	"net/http"
	"time"
)

// TailnetProxyPort is where ServeProxy listens, beside TailnetPort on the
// same address and behind the same whois check.
const TailnetProxyPort = 45877

// ServeProxy serves an HTTP CONNECT proxy on ln, so the iPhone app's browser
// can open the dev servers this machine runs: they bind 127.0.0.1, and
// tenant hosts like *.dev.invalid resolve only through this machine's
// /etc/hosts. Every name is resolved and dialled here, so a URL that works
// in the Mac's browser works on the phone unchanged.
//
// CONNECT only: WebKit's ProxyConfiguration tunnels plain http through
// CONNECT too, so a forward path for absolute-URI requests would serve
// nobody. It reaches whatever this machine can, which is no more than the
// IPC port beside it already allows the same peer (CreateSession runs
// commands).
func ServeProxy(ln net.Listener) error {
	return (&http.Server{Handler: http.HandlerFunc(connect), ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}

func connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "moomux proxy: CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	dst, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		// The browser shows this, so it says which server isn't up.
		http.Error(w, "moomux proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	c, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = dst.Close()
		return
	}
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	// A side that finishes sending half-closes the other, which still gets
	// to answer: a client that shuts its write side after the request must
	// not lose the response. The tunnel ends when both directions have.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dst, rw); closeWrite(dst); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, dst); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
	_ = c.Close()
	_ = dst.Close()
}

// closeWrite half-closes c, or fully closes a conn that can't half-close.
func closeWrite(c net.Conn) {
	if hc, ok := c.(interface{ CloseWrite() error }); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = c.Close()
}
