package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TailnetPort is the TCP port ListenTailnet binds on this machine's own
// Tailscale address. Fixed rather than configurable: the only clients are
// moomux's own, and one less thing to get wrong on both ends.
const TailnetPort = 45876

// tailscaleTimeout bounds each `tailscale` subprocess. A wedged CLI would
// otherwise hold up every connection from that peer, since a connection is
// admitted only once its authorization check finishes.
var tailscaleTimeout = 5 * time.Second

// ListenTailnet binds a second listener on this machine's Tailscale address,
// so a phone on the same tailnet can reach the same core the unix socket
// serves locally.
//
// It binds the tailnet address specifically and never 0.0.0.0: this wire
// carries CreateSession, which runs the worktree-create userscripts and can
// launch an agent with its permission-skipping flag. WireGuard gives
// encryption and machine identity, so there is no TLS and no pairing flow to
// design — but it does not give *authorization*, since every node on the
// tailnet can reach the bind. That is what the whois check below is for.
//
// Deliberately not tsnet. Becoming a tailnet node of its own is the more
// correct answer — it never touches the host's network stack, so there is no
// way to fat-finger a bind onto the LAN — but it pulls the whole
// tailscale.com module into a go.mod with ten direct requirements. Revisit
// if binding proves fragile when the interface is down.
//
// Every failure here is a reason to serve the unix socket alone, not to fail
// to start: no tailscale on the machine, the daemon stopped, a logged-out
// node. The caller logs and carries on.
func ListenTailnet(port int) (net.Listener, error) {
	addr, err := tailscaleSelfIPFn()
	if err != nil {
		return nil, err
	}
	owner, err := tailscaleLoginFn(addr)
	if err != nil {
		return nil, fmt.Errorf("tailscale whois %s: %w", addr, err)
	}
	// A tag-owned node has no user: whois reports "tagged-devices", and
	// admitting every peer whose login matches that would open CreateSession
	// to every tagged node on the tailnet. Serve the unix socket alone, the
	// same as for any other whois failure.
	if !strings.Contains(owner, "@") {
		return nil, fmt.Errorf("tailscale whois %s: not user-owned (%q)", addr, owner)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(addr, fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	return newTailnetListener(ln, owner), nil
}

// tailnetListener refuses every peer that isn't the tailnet user this node
// belongs to. `tailscale whois` is the authority — the core already shells
// out to git, tmux and gh constantly, so one more is in keeping — and the
// peer address comes from the accepted connection rather than from anything
// the peer said, so it can't be claimed.
type tailnetListener struct {
	net.Listener
	owner string

	// The check is a subprocess, so it runs per connection off the accept
	// path rather than inside Accept. Inline, one slow or wedged
	// `tailscaled` would stall every *other* connection behind it for the
	// whole timeout — and since the protocol is connection-per-call, that is
	// the phone locked out, not merely delayed.
	conns     chan net.Conn
	fail      chan error
	done      chan struct{}
	once      sync.Once
	closeOnce sync.Once

	// A small result cache. Positives spare a fork per call on a protocol
	// that opens a connection per call; negatives are cached far more
	// briefly, only to blunt a connection storm into something that doesn't
	// fork `tailscale` per packet. The cost of the positive TTL is that a
	// node that leaves the tailnet stays authorized for up to that long.
	mu sync.Mutex
	ok map[string]*authEntry
}

type authEntry struct {
	// Held across the whois, so N connections arriving together from one
	// peer cost one fork rather than N — the storm the refusal cache is
	// there for is concurrent, not sequential. Per peer and not global:
	// a wedged check for one node must not stall any other.
	gate sync.Mutex
	ok   bool
	at   time.Time
}

const (
	tailnetAuthTTL    = time.Minute
	tailnetRefusalTTL = 5 * time.Second
)

func newTailnetListener(ln net.Listener, owner string) *tailnetListener {
	return &tailnetListener{
		Listener: ln,
		owner:    owner,
		conns:    make(chan net.Conn),
		fail:     make(chan error, 1),
		done:     make(chan struct{}),
		ok:       map[string]*authEntry{},
	}
}

func (l *tailnetListener) Accept() (net.Conn, error) {
	l.once.Do(func() { go l.pump() })
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.fail:
		l.fail <- err // sticky: every later Accept reports the same end
		return nil, err
	case <-l.done:
		// Closed before the pump ever ran, or while its error was being
		// dropped below. Without this Accept would block forever on a
		// listener nobody is feeding.
		return nil, net.ErrClosed
	}
}

// Close stops the pump and releases any connection still being checked.
func (l *tailnetListener) Close() error {
	l.once.Do(func() {}) // never start a pump after Close
	// Once, not a check-then-act: net.Listener.Close is conventionally
	// idempotent and safe to call concurrently, and two callers reaching a
	// bare close(l.done) together would panic.
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// pump accepts continuously and hands each connection to its own
// authorization goroutine, so a refusal (or a slow check) delays nobody.
func (l *tailnetListener) pump() {
	backoff := 5 * time.Millisecond
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			// Out of file descriptors, or a peer that hung up mid-handshake:
			// the listener is fine and retrying works. Returning here would
			// leave the core up and healthy-looking with the phone locked
			// out until a restart, on the strength of one transient errno.
			if transientAccept(err) {
				select {
				case <-time.After(backoff):
				case <-l.done:
					return
				}
				if backoff *= 2; backoff > time.Second {
					backoff = time.Second
				}
				continue
			}
			// fail is buffered, and a second terminal error cannot happen:
			// this returns. Never select on done as well — both cases are
			// ready after Close and the error would be dropped at random.
			select {
			case l.fail <- err:
			default:
			}
			return
		}
		backoff = 5 * time.Millisecond
		go l.admit(c)
	}
}

func (l *tailnetListener) admit(c net.Conn) {
	peer, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil || !l.authorized(peer) {
		slog.Warn("ipc: refused tailnet peer", "peer", c.RemoteAddr().String())
		_ = c.Close()
		return
	}
	select {
	case l.conns <- c:
	case <-l.done:
		_ = c.Close()
	}
}

func (l *tailnetListener) authorized(peer string) bool {
	e := l.entry(peer)
	e.gate.Lock()
	defer e.gate.Unlock()
	l.mu.Lock()
	ok, at := e.ok, e.at
	l.mu.Unlock()
	ttl := tailnetRefusalTTL
	if ok {
		ttl = tailnetAuthTTL
	}
	if !at.IsZero() && time.Since(at) < ttl {
		return ok
	}
	login, err := tailscaleLoginFn(peer)
	allow := err == nil && strings.EqualFold(login, l.owner)
	l.mu.Lock()
	e.ok, e.at = allow, time.Now()
	l.mu.Unlock()
	return allow
}

// entry returns peer's cache slot, creating it, and drops the slots nothing
// has asked about for a whole TTL — the map is keyed by address and would
// otherwise only grow. TryLock skips a slot with a check in flight, whose
// timestamp is stale precisely because it hasn't been written yet.
func (l *tailnetListener) entry(peer string) *authEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	for p, old := range l.ok {
		// A zero at is a slot whose first check hasn't landed yet, not a
		// stale one: evicting it would orphan the entry authorized is about
		// to write, and the next connection from that peer would fork again.
		if p == peer || old.at.IsZero() || time.Since(old.at) < tailnetAuthTTL {
			continue
		}
		if old.gate.TryLock() {
			old.gate.Unlock()
			delete(l.ok, p)
		}
	}
	e := l.ok[peer]
	if e == nil {
		e = &authEntry{}
		l.ok[peer] = e
	}
	return e
}

// tailscaleSelfIPFn is tailscaleSelfIP, as a variable so a test can answer
// for a tailnet it isn't on.
var tailscaleSelfIPFn = tailscaleSelfIP

// tailscaleSelfIP is this node's own tailnet address — the only thing the
// listener may bind.
func tailscaleSelfIP() (string, error) {
	out, err := tailscaleRun("ip", "-4")
	if err != nil {
		return "", err
	}
	addr, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if net.ParseIP(addr) == nil {
		return "", fmt.Errorf("tailscale ip: no address (%q)", strings.TrimSpace(out))
	}
	return addr, nil
}

// tailscaleLoginFn is tailscaleLogin, as a variable so a test can answer for
// a tailnet it isn't on.
var tailscaleLoginFn = tailscaleLogin

// tailscaleLogin returns the tailnet user that owns the node at addr.
func tailscaleLogin(addr string) (string, error) {
	out, err := tailscaleRun("whois", "--json", addr)
	if err != nil {
		return "", err
	}
	var who struct {
		UserProfile struct {
			LoginName string
		}
	}
	if err := json.Unmarshal([]byte(out), &who); err != nil {
		return "", err
	}
	if who.UserProfile.LoginName == "" {
		return "", errors.New("no login name")
	}
	return who.UserProfile.LoginName, nil
}

// tailscaleCandidates are tried in order when `tailscale` isn't on PATH. The
// Homebrew service's launchd plist sets a PATH of only /opt/homebrew and the
// system dirs, while Tailscale's macOS app puts its CLI shim in /usr/local/bin
// or leaves it inside the bundle — so a core started that way never found it.
var tailscaleCandidates = []string{
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
}

// tailscaleBin resolves the CLI once. With nothing found it returns the bare
// name, so the exec error still reads "not found in $PATH".
var tailscaleBin = sync.OnceValue(func() string { return findTailscale(tailscaleCandidates) })

// findTailscale returns the first of PATH's `tailscale` and candidates that
// exists and is executable. LookPath on an absolute path does that check.
func findTailscale(candidates []string) string {
	for _, name := range append([]string{"tailscale"}, candidates...) {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "tailscale"
}

func tailscaleRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tailscaleTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, tailscaleBin(), args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// transientAccept reports whether an Accept error is worth retrying rather
// than ending the listener. net.Error's Temporary() would say the same thing
// but is deprecated, and the set that actually matters here is small.
func transientAccept(err error) bool {
	for _, e := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.ECONNABORTED, syscall.ECONNRESET, syscall.ETIMEDOUT} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
