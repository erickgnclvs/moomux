package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
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
// encryption and machine identity, so there is no TLS and no credential for
// pairing to hand out — but it does not give *authorization*, since every
// node on the tailnet can reach the bind. That is what the whois check below
// is for.
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

// tailnetRetryMin and tailnetRetryMax bound ListenTailnetRetry's backoff.
// Variables so a test needn't wait out real seconds.
var tailnetRetryMin, tailnetRetryMax = 2 * time.Second, time.Minute

// ListenTailnetRetry is ListenTailnet, retried with capped backoff until it
// succeeds. A launchd-started core comes up at login before Tailscale does,
// and giving up on the first `tailscale ip` left the phone locked out until
// someone restarted the service. It blocks, so call it from a goroutine.
//
// onErr hears every failure, so a caller can show the latest — by the time
// anyone asks, the first can be as old as the core — and decide for itself
// how often to log. A receive on wake retries at once rather than sleeping
// out the backoff, which by then is a minute; a nil wake never fires.
func ListenTailnetRetry(port int, wake <-chan struct{}, onErr func(error)) net.Listener {
	wait := tailnetRetryMin
	for {
		ln, err := ListenTailnet(port)
		if err == nil {
			return ln
		}
		onErr(err)
		select {
		case <-time.After(wait):
		case <-wake:
		}
		wait = min(wait*2, tailnetRetryMax)
	}
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
	// Shared with any sibling, so a phone using both ports costs one fork.
	mu *sync.Mutex
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
		mu:       &sync.Mutex{},
		ok:       map[string]*authEntry{},
	}
}

// sibling wraps ln behind the same check as l: the same owner and the same
// result cache.
func (l *tailnetListener) sibling(ln net.Listener) *tailnetListener {
	s := newTailnetListener(ln, l.owner)
	s.mu, s.ok = l.mu, l.ok
	return s
}

// Owner is the tailnet login this listener admits, fixed when it bound.
func (l *tailnetListener) Owner() string { return l.owner }

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
		slog.Warn("ipc: refused tailnet peer", "peer", c.RemoteAddr().String(), "on", l.Addr().String())
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
	out, err := tailscaleRunFn("ip", "-4")
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
	out, err := tailscaleRunFn("whois", "--json", addr)
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

// The three ways this machine can be unfit to pair with a phone. Their text
// crosses the wire and both front ends print it verbatim, so it is the whole
// user-facing message: tailnetSelf returns these bare, never wrapped.
var (
	//lint:ignore ST1005 user-facing sentence, shown verbatim
	errNoTailscale = errors.New("Moomux reaches your iPhone through Tailscale. Install it on this computer and on your iPhone — https://tailscale.com/download — sign in to the same account on both, then try again.")
	//lint:ignore ST1005 user-facing sentence, shown verbatim
	errTailscaleOff = errors.New("Tailscale is off on this computer. Open it, sign in, and try again.")
	//lint:ignore ST1005 user-facing sentence, shown verbatim
	errTaggedNode = errors.New("This computer is a tagged Tailscale device; moomux needs it signed in as a person.")
)

// TailnetSelf is what Tailscale says about this machine: the address the
// listener binds, the person it is signed in as, and its host name.
type TailnetSelf struct{ IP, Owner, Name string }

// tailnetSelfFn is tailnetSelf, as a variable so a test can answer for a
// tailnet it isn't on.
var tailnetSelfFn = tailnetSelf

// tailnetSelf asks Tailscale about this machine, mapping the failures a user
// can fix onto the sentinel errors above.
//
// `status --json` goes first and its BackendState is checked, because
// `tailscale ip -4` keeps answering with the node's address while Tailscale is
// stopped — and whois then fails with "peer not found", which would reach the
// user as a raw exit status instead of "Tailscale is off".
func tailnetSelf() (TailnetSelf, error) {
	out, err := tailscaleRunFn("status", "--json")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return TailnetSelf{}, errNoTailscale
		}
		return TailnetSelf{}, errTailscaleOff
	}
	var st struct {
		BackendState string
		Self         struct {
			HostName string
		}
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return TailnetSelf{}, fmt.Errorf("tailscale status: %w", err)
	}
	if st.BackendState != "Running" {
		return TailnetSelf{}, errTailscaleOff
	}
	ip, err := tailscaleSelfIP()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return TailnetSelf{}, errNoTailscale
		}
		return TailnetSelf{}, errTailscaleOff
	}
	owner, err := tailscaleLogin(ip)
	if err != nil {
		return TailnetSelf{}, fmt.Errorf("tailscale whois %s: %w", ip, err)
	}
	if !strings.Contains(owner, "@") {
		return TailnetSelf{}, errTaggedNode
	}
	return TailnetSelf{IP: ip, Owner: owner, Name: st.Self.HostName}, nil
}

// PairLink is the URL the iPhone scans to add this machine. A space
// is %20 rather than the + that url.Values would write, which the phone's
// URL parser would keep literally.
func PairLink(ip, name string) string {
	q := url.Values{"host": {ip}, "name": {name}}.Encode()
	return "moomux://connect?" + strings.ReplaceAll(q, "+", "%20")
}

// pairBindWait is how long Pair waits for the listener to bind before
// saying so. A variable so a test needn't wait it out.
var pairBindWait = 5 * time.Second

// errTailnetChanged is Pair's answer when the running listener no longer
// matches this machine: it bound an address and admits an owner, both
// fixed at bind time, and a re-login or account switch since has moved
// one of them. Bare for the same reason as the sentinels above.
//
//lint:ignore ST1005 user-facing sentence, shown verbatim
var errTailnetChanged = errors.New("Tailscale changed since moomux started. Restart it (`brew services restart moomux`), then try again.")

// Tailnet is the core's tailnet listener: started at most once — by
// `moomux serve` at startup when tailnet_listen is already on, and by Pair
// when a front end asks for it — and serving Srv for the life of the
// process.
type Tailnet struct {
	Srv *Server
	// ProxyPort, when set, also serves ServeProxy on that port, beside the
	// listener and behind its check. Zero is no proxy.
	ProxyPort int
	// listen binds the listener, blocking until it can; onErr hears each
	// reason it can't yet, and a receive on wake is a reason to try again
	// now. Nil is ListenTailnetRetry on TailnetPort — a field so a test can
	// bind loopback rather than a tailnet it isn't on.
	listen func(wake <-chan struct{}, onErr func(error)) net.Listener

	start sync.Once
	// bound is closed once ln is set. ln is written only before that and
	// read only after, so the close is all the synchronisation it needs.
	bound chan struct{}
	ln    net.Listener
	// wake cuts a retry's backoff short for Pair. Buffered by one and sent
	// without blocking: one pending wake is as good as several, and once
	// the listener is bound nothing reads it.
	wake chan struct{}

	mu      sync.Mutex
	lastErr error // the latest reason it couldn't bind, for Pair to show
}

// Start binds and serves the tailnet listener in the background, once:
// every later call is a no-op, so serve's startup and any number of Pair
// calls can all ask for it. Never fatal — until it binds, the unix socket
// is the only way in, which is what local clients use either way.
func (t *Tailnet) Start() {
	t.start.Do(func() {
		t.bound = make(chan struct{})
		t.wake = make(chan struct{}, 1)
		listen := t.listen
		if listen == nil {
			listen = func(wake <-chan struct{}, onErr func(error)) net.Listener {
				return ListenTailnetRetry(TailnetPort, wake, onErr)
			}
		}
		go t.serve(listen)
	})
}

func (t *Tailnet) serve(listen func(wake <-chan struct{}, onErr func(error)) net.Listener) {
	ln := listen(t.wake, func(err error) {
		t.mu.Lock()
		first := t.lastErr == nil
		t.lastErr = err
		t.mu.Unlock()
		// Logged once, so a node that is never on a tailnet says so once
		// rather than every minute.
		if first {
			fmt.Fprintln(os.Stderr, "moomux: no tailnet listener yet, retrying:", err)
		}
	})
	// Serve returning means nothing is accepting on this listener any
	// more: close it, or the port stays bound and any connection already
	// past the tailnet auth check sits on its fd until the process exits.
	defer ln.Close()
	// Bound before t.bound closes, so a Pair that succeeds hands the phone
	// a browser that is already up.
	if pl := t.bindProxy(ln); pl != nil {
		defer pl.Close()
		go func() {
			if err := ServeProxy(pl); !errors.Is(err, net.ErrClosed) {
				slog.Error("tailnet browser proxy stopped", "err", err)
			}
		}()
	}
	t.ln = ln
	close(t.bound)
	fmt.Fprintln(os.Stderr, "moomux: also serving on", ln.Addr())
	// Serve only ever returns a non-nil error.
	if err := t.Srv.Serve(ln); !errors.Is(err, net.ErrClosed) {
		slog.Error("tailnet listener stopped", "err", err)
	}
}

// bindProxy binds ProxyPort on ln's address, behind ln's own check, or
// returns nil. It asks Tailscale nothing: ln already holds the address and
// owner. Never fatal, and Pair never hears of it: a proxy that can't bind
// costs the phone its browser and nothing else.
func (t *Tailnet) bindProxy(ln net.Listener) net.Listener {
	if t.ProxyPort == 0 {
		return nil
	}
	// A listener that can't say whom it admits gets no proxy: fail closed.
	tl, _ := ln.(*tailnetListener)
	if tl == nil {
		return nil
	}
	host, _, _ := net.SplitHostPort(tl.Addr().String())
	raw, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(t.ProxyPort)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "moomux: no tailnet browser proxy:", err)
		return nil
	}
	fmt.Fprintln(os.Stderr, "moomux: browser proxy on", raw.Addr())
	return tl.sibling(raw)
}

// Pair makes sure the listener is up and answers what a phone needs to
// reach it. enable saves tailnet_listen and reports whether it was off.
//
// The order is the point. Tailscale is asked live, every call, so a
// machine whose listener bound at login and whose Tailscale was quit
// since gets "Tailscale is off", not a QR code that can't work. Then the
// listener must actually be up, and still bound to what Tailscale says
// now. A stale one is reported rather than rebound: an account switch is
// rare, and a restart fixes it without a restartable listener to keep.
// enable runs only after all of that, so a failed Pair saves nothing: no
// core quietly opening on the tailnet at its next start.
func (t *Tailnet) Pair(enable func() (bool, error)) (Pairing, error) {
	self, err := tailnetSelfFn()
	if err != nil {
		return Pairing{}, err
	}
	t.Start()
	// Someone is waiting on this answer. A retry asleep in its backoff —
	// Tailscale was down at login and has come back since — tries again
	// now, not after Pair has given up on it.
	select {
	case t.wake <- struct{}{}:
	default:
	}
	select {
	case <-t.bound:
	case <-time.After(pairBindWait):
		t.mu.Lock()
		why := t.lastErr
		t.mu.Unlock()
		// Nothing heard is a `tailscale` call still inside its timeout:
		// say how long was waited rather than show an empty "()".
		reason := fmt.Sprintf("no answer after %s", pairBindWait)
		if why != nil {
			reason = why.Error()
		}
		//lint:ignore ST1005 user-facing sentence, shown verbatim
		return Pairing{}, fmt.Errorf("moomux couldn't listen on Tailscale yet (%s). Try again in a moment.", reason)
	}
	host, _, _ := net.SplitHostPort(t.ln.Addr().String())
	// A listener that can't say whom it admits can't be shown to match,
	// so it fails closed.
	tl, _ := t.ln.(*tailnetListener)
	if tl == nil || host != self.IP || !strings.EqualFold(tl.Owner(), self.Owner) {
		return Pairing{}, errTailnetChanged
	}
	changed, err := enable()
	if err != nil {
		return Pairing{}, err
	}
	return Pairing{Link: PairLink(self.IP, self.Name), Name: self.Name, Owner: self.Owner, TurnedOn: changed}, nil
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

// tailscaleRunFn is tailscaleRun, as a variable so a test can answer for a
// tailnet it isn't on.
var tailscaleRunFn = tailscaleRun

func tailscaleRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tailscaleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBin(), args...)
	// The macOS app's binary is the CLI only when it looks run from a shell
	// (SHLVL or TERM set). A launchd-started core has neither, so without
	// this it starts as the GUI instead and prints "The Tailscale GUI failed
	// to start" on stdout with exit 0. Tailscale's own /usr/local/bin shim
	// works only because sh sets SHLVL.
	cmd.Env = append(os.Environ(), "SHLVL=1")
	out, err := cmd.Output()
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
