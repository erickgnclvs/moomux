package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

func TestCaptureAndReviewRoundTrip(t *testing.T) {
	b := &fakeBackend{sessions: []session.Session{{ID: "moomux:a"}, {ID: "moomux:b"}}}
	c, _ := start(t, b, &config.Config{}, nil)

	got := c.Capture([]string{"moomux:a", "moomux:b"})
	want := map[string]string{"moomux:a": "screen of moomux:a", "moomux:b": "screen of moomux:b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Capture = %#v", got)
	}
	hint, err := c.Review("moomux:a")
	if err != nil || hint != "reviewing moomux:a" {
		t.Fatalf("Review = %q, %v", hint, err)
	}
}

// A front end polls Capture on a timer to redraw a grid. A failed call must
// come back empty rather than nil-with-a-panic, and must not read to the
// caller as "every pane is blank".
func TestCaptureOnADeadSocketIsEmpty(t *testing.T) {
	c := &Client{Socket: t.TempDir() + "/nope.sock"}
	if got := c.Capture([]string{"moomux:a"}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// Attach's error path is the only part of it that is still JSON: a failure
// has to arrive as a response line, because after a success the connection
// is raw pty bytes and there is nowhere to put an error.
func TestAttachUnknownSessionErrorsBeforeGoingRaw(t *testing.T) {
	b := &fakeBackend{sessions: []session.Session{{ID: "moomux:a", TmuxSession: "moomux-a"}}}
	c, _ := start(t, b, &config.Config{}, nil)
	att, err := c.Attach("moomux:gone", 100, 40)
	if err == nil {
		att.Close()
		t.Fatal("attaching to an unknown session must fail")
	}
	if !strings.Contains(err.Error(), "moomux:gone") {
		t.Fatalf("err = %v", err)
	}
}

// A session the core knows but that has no tmux session name is the other
// pre-raw failure — attaching to "" would target whatever tmux picked.
func TestAttachWithoutATmuxSessionErrors(t *testing.T) {
	b := &fakeBackend{sessions: []session.Session{{ID: "moomux:a"}}}
	c, _ := start(t, b, &config.Config{}, nil)
	if _, err := c.Attach("moomux:a", 80, 24); err == nil {
		t.Fatal("expected an error for a session with no tmux session")
	}
}

// `moomux ui -socket 100.x.y.z:45876` is how the tailnet listener is tested
// without a phone, so the client has to reach both transports — and a socket
// path must never be mistaken for an address.
func TestClientDialsTCPOnlyForAHostPort(t *testing.T) {
	b := &fakeBackend{sessions: []session.Session{{ID: "moomux:a"}}}
	_, ln := start(t, b, &config.Config{}, nil)
	_ = ln

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpLn.Close()
	go (&Server{Backend: b, Config: func() config.Config { return config.Config{} }}).Serve(tcpLn)

	over := &Client{Socket: tcpLn.Addr().String()}
	if got := over.Sessions(); len(got) != 1 || got[0].ID != "moomux:a" {
		t.Fatalf("over tcp: %v", got)
	}
	// A path that happens to carry a colon is still a path.
	if _, err := (&Client{Socket: "/tmp/moomux:1/x.sock"}).dial(); err == nil {
		t.Fatal("expected a unix dial failure, not a tcp dial")
	} else if strings.Contains(err.Error(), "tcp") {
		t.Fatalf("dialled tcp for a path: %v", err)
	}
	// ...including one with no slash at all to settle it, which is any
	// relative socket name. SplitHostPort accepts "moomux:1.sock" happily;
	// only the port failing to parse as a number keeps it off tcp.
	if _, err := (&Client{Socket: "moomux:1.sock"}).dial(); err == nil {
		t.Fatal("expected a unix dial failure, not a tcp dial")
	} else if strings.Contains(err.Error(), "tcp") {
		t.Fatalf("dialled tcp for a relative path: %v", err)
	}
}

// The handshake's one sharp edge: the server writes the response line and
// then immediately starts copying tmux's first screen draw, so a single
// read() on the client hands back both. json.Decoder refills in 512-byte
// chunks and a bufio.Reader asked for 512 fills its own 4096 first, so
// everything between those two sizes sits in the bufio buffer where
// dec.Buffered() can't see it. Reading from the connection afterwards skips
// straight past it — a hole in the middle of the first frame, which renders
// as a mangled screen until something forces a redraw.
func TestAttachKeepsEveryByteWrittenBehindTheResponseLine(t *testing.T) {
	// A server that answers the handshake and dumps a known payload in the
	// same breath. 8KB is past both the decoder's chunk and bufio's buffer.
	want := bytes.Repeat([]byte("0123456789abcdef"), 512)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n') // the request
		var out bytes.Buffer
		out.WriteString(`{"result":{"ok":true}}` + "\n")
		out.Write(want)
		_, _ = conn.Write(out.Bytes()) // one write: response + first frame
		time.Sleep(200 * time.Millisecond)
	}()

	att, err := (&Client{Socket: ln.Addr().String()}).Attach("moomux:a", 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(att, got); err != nil {
		t.Fatalf("read %d of %d bytes: %v", len(got), len(want), err)
	}
	if !bytes.Equal(got, want) {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("first frame diverges at byte %d: got %q, want %q", i, got[i:min(i+24, len(got))], want[i:min(i+24, len(want))])
			}
		}
	}
}

// The mirror case, and not a rare one: the response line arrives in a packet
// of its own and the screen draw comes later, so there is nothing behind the
// newline at all. A reader that can't tell "line complete, remainder empty"
// from "no line yet" blocks here forever waiting for bytes it already has —
// and a quiet session is exactly what produces it.
func TestAttachHandlesAResponseLineWithNothingBehindIt(t *testing.T) {
	want := []byte("drawn later")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_, _ = conn.Write([]byte(`{"result":{"ok":true}}` + "\n")) // alone
		time.Sleep(250 * time.Millisecond)
		_, _ = conn.Write(want)
		time.Sleep(250 * time.Millisecond)
	}()

	att, err := (&Client{Socket: ln.Addr().String()}).Attach("moomux:a", 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(att, got); err != nil {
		t.Fatalf("read after an empty remainder: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A client that writes its request and then reads the answer on the same
// connection — no trailing newline, no half-close — is an ordinary way to
// write one, and it is what the Swift client does. Requiring a newline or
// an EOF made every pull method hang for it: no error, no refusal, nothing,
// on every method at once. That is the worst shape a protocol change can
// take, so the server parses a complete JSON value and asks for neither.
func TestPullMethodAnswersAClientThatNeitherTerminatesNorHalfCloses(t *testing.T) {
	b := &fakeBackend{sessions: []session.Session{{ID: "moomux:a"}}}
	_, ln := start(t, b, &config.Config{}, nil)
	_ = ln

	conn, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// No "\n", and the write half stays open.
	if _, err := conn.Write([]byte(`{"method":"Sessions"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no answer to an unterminated request: %v", err)
	}
	var res response
	if err := json.Unmarshal(line, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Result.Sessions) != 1 {
		t.Fatalf("got %+v", res.Result)
	}
}

// The other half of that trade: tolerating a request with no terminator must
// not put the terminator of one that has it into the pty.
func TestAttachInputDropsOnlyTheRequestTerminator(t *testing.T) {
	for _, tc := range []struct{ name, tail, want string }{
		{"lf", "\nkeys", "keys"},
		{"crlf", "\r\nkeys", "keys"},
		{"none", "keys", "keys"},
		{"bare cr is a keypress, not framing", "\rkeys", "\rkeys"},
		{"only the first newline", "\n\nkeys", "\nkeys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.NewReader(`{"method":"Attach"}` + tc.tail)
			dec := json.NewDecoder(src)
			var req request
			if err := dec.Decode(&req); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(attachInput(dec, src))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A zero-sized pty makes tmux draw nothing at all, which reads as a hung
// connection rather than as a bad request — so an unsaid or nonsensical size
// becomes 80x24, never 0.
func TestWinsizeDefaultsRatherThanZero(t *testing.T) {
	for _, tc := range []struct{ cols, rows, wantC, wantR int }{
		{0, 0, 80, 24},
		{-1, -1, 80, 24},
		{1 << 20, 1 << 20, 80, 24},
		{100, 40, 100, 40},
	} {
		got := winsize(tc.cols, tc.rows)
		if int(got.Cols) != tc.wantC || int(got.Rows) != tc.wantR {
			t.Fatalf("winsize(%d,%d) = %dx%d", tc.cols, tc.rows, got.Cols, got.Rows)
		}
	}
}

// The tailnet listener is the whole authorization boundary: WireGuard proves
// which machine is calling, nothing more, and every node on the tailnet can
// reach the bind. A peer whose tailnet user isn't this node's owner must have
// its connection closed rather than reaching a core with CreateSession on it
// — and the refusal must not stop the listener, or one stranger would be a
// denial of service.
func TestTailnetListenerRefusesAnotherTailnetUser(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()

	// Both peers dial from 127.0.0.1, so the sequence is what distinguishes
	// them: the first connection is a stranger's node, the second is ours.
	var mu sync.Mutex
	logins := []string{"someone-else@example.com", "ME@example.com"}
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(logins) == 0 {
			return "", errors.New("no such node")
		}
		login := logins[0]
		logins = logins[1:]
		return login, nil
	}
	defer func() { tailscaleLoginFn = restore }()

	// A fresh listener, so both connections really are checked.
	ln := newTailnetListener(inner, "me@example.com")
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- c
		}
	}()

	dial := func() net.Conn {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	stranger := dial()
	defer stranger.Close()
	// Wait for the refusal before dialing again: admission runs off the
	// accept path now, so this is what orders the two connections against
	// the answers the fake hands out.
	_ = stranger.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := stranger.Read(make([]byte, 1)); err == nil {
		t.Fatalf("stranger was not disconnected (read %d bytes)", n)
	}

	// Both "peers" here dial from 127.0.0.1, which production cannot
	// produce — two tailnet users never share a source address — so the
	// per-IP result cache would otherwise answer for the second connection
	// with the first one's refusal.
	ln.mu.Lock()
	ln.ok = map[string]*authEntry{}
	ln.mu.Unlock()

	ours := dial()
	defer ours.Close()
	select {
	case c, ok := <-accepted:
		if !ok {
			t.Fatal("listener stopped after refusing a peer")
		}
		defer c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("authorized peer was never accepted")
	}
	if len(accepted) != 0 {
		t.Fatal("the unauthorized peer was accepted too")
	}
}

// A refusal is cached as well, far more briefly: long enough that a peer
// opening connections in a loop cannot fork `tailscale` per connection,
// short enough that a node authorized a moment later isn't locked out.
func TestTailnetListenerCachesARefusalBriefly(t *testing.T) {
	var calls int
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) { calls++; return "someone-else@example.com", nil }
	defer func() { tailscaleLoginFn = restore }()

	ln := newTailnetListener(nil, "me@example.com")
	for range 3 {
		if ln.authorized("100.64.0.9") {
			t.Fatal("want refused")
		}
	}
	if calls != 1 {
		t.Fatalf("whois ran %d times for a repeat offender, want 1", calls)
	}
	// ...and it really does expire, rather than banning the peer forever.
	ln.mu.Lock()
	ln.ok["100.64.0.9"].at = time.Now().Add(-2 * tailnetRefusalTTL)
	ln.mu.Unlock()
	_ = ln.authorized("100.64.0.9")
	if calls != 2 {
		t.Fatalf("a stale refusal was not rechecked (calls=%d)", calls)
	}
}

// The allow-cache is what keeps a poll-every-few-seconds client from forking
// `tailscale` at the same rate inside Accept.
func TestTailnetListenerCachesAnAuthorizedPeer(t *testing.T) {
	var calls int
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) { calls++; return "me@example.com", nil }
	defer func() { tailscaleLoginFn = restore }()

	ln := newTailnetListener(nil, "me@example.com")
	for range 3 {
		if !ln.authorized("100.64.0.1") {
			t.Fatal("want authorized")
		}
	}
	if calls != 1 {
		t.Fatalf("whois ran %d times, want 1", calls)
	}
}

// Over TCP the decoder can stop at the closing brace with nothing buffered,
// and the terminator then arrives on the connection itself. Stripping only
// what dec.Buffered() held would type an Enter into the agent's pane.
func TestAttachInputDropsATerminatorThatArrivesLate(t *testing.T) {
	src := io.MultiReader(strings.NewReader(`{"method":"Attach"}`), strings.NewReader("\nkeys"))
	dec := json.NewDecoder(src)
	var req request
	if err := dec.Decode(&req); err != nil {
		t.Fatal(err)
	}
	if n := dec.Buffered().(interface{ Len() int }).Len(); n != 0 {
		t.Fatalf("this test needs the terminator to arrive after the decode, buffered %d", n)
	}
	got, err := io.ReadAll(attachInput(dec, src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keys" {
		t.Fatalf("got %q, want %q", got, "keys")
	}
}

// Close must end Accept. Both the pump's error and done are ready once the
// listener is closed, so a select over the two drops the error half the time
// and leaves Serve's Accept parked forever.
func TestTailnetListenerAcceptReturnsAfterClose(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"closed before the first accept", "closed while accepting"} {
		t.Run(name, func(t *testing.T) {
			ln := newTailnetListener(inner, "me@example.com")
			done := make(chan error, 1)
			if name == "closed while accepting" {
				go func() { _, err := ln.Accept(); done <- err }()
				time.Sleep(50 * time.Millisecond)
				_ = ln.Close()
			} else {
				_ = ln.Close()
				go func() { _, err := ln.Accept(); done <- err }()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("want an error from a closed listener")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Accept never returned after Close")
			}
		})
	}
}

// The refusal cache is there to blunt a connection storm, and a storm is
// concurrent: admission runs per connection in its own goroutine, so N
// connections arriving together must still cost one `tailscale whois`.
func TestTailnetListenerChecksAPeerOnceUnderConcurrency(t *testing.T) {
	var mu sync.Mutex
	var calls int
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return "someone-else@example.com", nil
	}
	defer func() { tailscaleLoginFn = restore }()

	ln := newTailnetListener(nil, "me@example.com")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); ln.authorized("100.64.0.9") }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("whois ran %d times for one peer's connection storm, want 1", calls)
	}
}

// net.Listener.Close is conventionally idempotent and safe to call
// concurrently; a bare check-then-act around close(l.done) panics when two
// callers pass the check together.
func TestTailnetListenerCloseIsIdempotentUnderConcurrency(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newTailnetListener(inner, "me@example.com")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = ln.Close() }()
	}
	wg.Wait()
}

// flakyListener yields a canned sequence of Accept errors.
type flakyListener struct {
	net.Listener
	errs []error
	n    int
}

func (f *flakyListener) Accept() (net.Conn, error) {
	if f.n < len(f.errs) {
		f.n++
		return nil, f.errs[f.n-1]
	}
	return nil, net.ErrClosed
}

func (f *flakyListener) Addr() net.Addr { return &net.TCPAddr{} }
func (f *flakyListener) Close() error   { return nil }

// A transient Accept error — out of descriptors, a peer that hung up
// mid-handshake — must not end the listener. It used to, leaving the core up
// and healthy-looking with the tailnet door shut until a restart.
func TestTailnetListenerSurvivesATransientAcceptError(t *testing.T) {
	ln := newTailnetListener(&flakyListener{errs: []error{syscall.ECONNABORTED, syscall.EMFILE}}, "me@example.com")
	defer ln.Close()
	_, err := ln.Accept()
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept ended on a transient error: %v", err)
	}
}

// A tag-owned node's whois reports "tagged-devices" rather than a person, so
// taking it as the owner would admit every tagged node on the tailnet — CI
// runners included — to CreateSession and its permission-skipping flag.
func TestListenTailnetRefusesATagOwnedNode(t *testing.T) {
	restoreIP, restoreLogin := tailscaleSelfIPFn, tailscaleLoginFn
	tailscaleSelfIPFn = func() (string, error) { return "100.64.0.1", nil }
	tailscaleLoginFn = func(string) (string, error) { return "tagged-devices", nil }
	defer func() { tailscaleSelfIPFn, tailscaleLoginFn = restoreIP, restoreLogin }()

	ln, err := ListenTailnet(0)
	if err == nil {
		_ = ln.Close()
		t.Fatal("a tag-owned node must not open the tailnet listener")
	}
	if !strings.Contains(err.Error(), "tagged-devices") {
		t.Fatalf("error should name the login it refused: %v", err)
	}
}

// entry's sweep must not evict the slot it is about to hand out. A fresh
// slot has a zero timestamp because its whois hasn't landed yet, not because
// it is stale; evicting it orphans the entry the caller then writes into, and
// the peer forks `tailscale` again on its next connection.
func TestTailnetEntryKeepsASlotWhoseCheckIsStillInFlight(t *testing.T) {
	ln := newTailnetListener(nil, "me@example.com")
	fresh := ln.entry("100.64.0.9") // created, no result written yet
	ln.entry("100.64.0.2")          // a different peer: runs the sweep
	if got := ln.entry("100.64.0.9"); got != fresh {
		t.Fatal("the in-flight slot was swept and replaced")
	}
}

// Screens is omitempty, so a *successful* capture of nothing decodes to
// nil — and the caller writes into what it gets back.
func TestCaptureOfNothingIsWritable(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, &config.Config{}, nil)
	got := c.Capture(nil)
	got["moomux:a"] = "drawn" // panics on a nil map
}
