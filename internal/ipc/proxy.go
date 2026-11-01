package ipc

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// TailnetProxyPort is where ServeProxy listens, beside TailnetPort on the
// same address and behind the same whois check.
const TailnetProxyPort = 45877

// maxTunnels bounds the open tunnels. They hold two descriptors each in the
// core's own process, so a page opening hundreds would otherwise starve
// tmux, git and the IPC listener of descriptors along with it.
const maxTunnels = 256

// tunnelIdle closes a tunnel nothing has crossed in either direction for
// that long.
const tunnelIdle = 10 * time.Minute

// cgnat is Tailscale's address range: a name rebound into it would reach
// other tailnet nodes as this machine.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

var errRefusedTarget = errors.New("refused")

// ServeProxy serves an HTTP CONNECT proxy on ln, so the iPhone app's browser
// can open the dev servers this machine runs: they bind 127.0.0.1, under
// names only this machine resolves. Every name is resolved and dialled
// here, so a URL that works in the Mac's browser works on the phone
// unchanged.
//
// CONNECT only: WebKit's ProxyConfiguration tunnels plain http through
// CONNECT too, so a forward path for absolute-URI requests would serve
// nobody. It reaches whatever this machine can, which is no more than the
// IPC port beside it already allows the same peer (CreateSession runs
// commands). What it guards against is the pages that browser loads: see
// allowTarget.
func ServeProxy(ln net.Listener) error {
	p := &proxy{self: ln.Addr().String(), slots: make(chan struct{}, maxTunnels), idle: tunnelIdle}
	return (&http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}

type proxy struct {
	self  string // this listener's address, never a target
	slots chan struct{}
	idle  time.Duration
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "moomux proxy: CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	name, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "moomux proxy: "+err.Error(), http.StatusBadRequest)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "moomux proxy: too many open tunnels", http.StatusServiceUnavailable)
		return
	}
	// Checked in Control, on the address actually being connected to, so a
	// name can't resolve one way for the check and another for the dial.
	d := net.Dialer{Timeout: 10 * time.Second, Control: func(_, addr string, _ syscall.RawConn) error {
		return p.allowTarget(name, addr)
	}}
	dst, err := d.Dial("tcp", r.Host)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errRefusedTarget) {
			code = http.StatusForbidden
		}
		// The browser shows this, so it says which server isn't up.
		http.Error(w, "moomux proxy: "+err.Error(), code)
		return
	}
	c, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = dst.Close()
		return
	}
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	// Idle means idle both ways: a quiet request side under a streaming
	// response is a live tunnel.
	idle := time.AfterFunc(p.idle, func() { _ = c.Close(); _ = dst.Close() })
	defer idle.Stop()
	// A side that finishes sending half-closes the other, which still gets
	// to answer: a client that shuts its write side after the request must
	// not lose the response. The tunnel ends when both directions have.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dst, activity{rw, idle, p.idle}); closeWrite(dst); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, activity{dst, idle, p.idle}); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
	_ = c.Close()
	_ = dst.Close()
}

// allowTarget refuses two kinds of dial, given the name asked for and the
// address it resolved to.
//
// The moomux ports: a dial from here arrives from this machine's own
// tailnet address, which whois admits, so nothing should be laundered
// through it.
//
// A public name that resolves to a loopback, private or tailnet address:
// DNS rebinding. The browser never resolves through a CONNECT proxy, so it
// can't pin an answer, and a page on a name its attacker controls could
// flip that name to 127.0.0.1 between two requests and read local services
// as its own origin. Only names that can't be registered may point inward:
// IP literals, localhost and *.localhost (RFC 6761, which macOS resolves to
// loopback by itself), and *.invalid.
func (p *proxy) allowTarget(name, addr string) error {
	if addr == p.self || addr == net.JoinHostPort(hostOf(p.self), fmt.Sprint(TailnetPort)) {
		return fmt.Errorf("%w: %s is moomux itself", errRefusedTarget, addr)
	}
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return err
	}
	ip := ap.Addr().Unmap()
	inward := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || cgnat.Contains(ip)
	if !inward {
		return nil
	}
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	if _, err := netip.ParseAddr(n); err == nil || n == "localhost" || strings.HasSuffix(n, ".localhost") || strings.HasSuffix(n, ".invalid") {
		return nil
	}
	return fmt.Errorf("%w: %s resolves to %s, and only a local name may", errRefusedTarget, name, ip)
}

func hostOf(addr string) string {
	h, _, _ := net.SplitHostPort(addr)
	return h
}

// activity is a Reader that pushes the tunnel's idle deadline back on
// every read that carries data.
type activity struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (a activity) Read(b []byte) (int, error) {
	n, err := a.r.Read(b)
	if n > 0 {
		a.timer.Reset(a.idle)
	}
	return n, err
}

// closeWrite half-closes c, or fully closes a conn that can't half-close.
func closeWrite(c net.Conn) {
	if hc, ok := c.(interface{ CloseWrite() error }); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = c.Close()
}
