package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/creack/pty"

	"github.com/erickgnclvs/moomux/internal/tmux"
)

// Attach is the one method whose connection stops being the protocol.
//
// The client sends a request line as usual and reads one response line back;
// from there the connection *is* the pty — raw bytes in both directions,
// forever. No framing, no length prefixes, no multiplexing, and closing the
// socket is the detach. A phone cannot run `tmux attach` itself, and this is
// the whole of what it needs instead.
//
// The initial terminal size rides the request (Args.Cols/Rows), which is the
// cheap half of resize: a client that changes size mid-attach detaches and
// reattaches. A control connection or an in-band escape is the upgrade if
// that ever grates.
//
// ponytail: no later resize path. Add one when a client actually rotates a
// phone mid-session and the reattach shows.
func (s *Server) attach(c net.Conn, r io.Reader, a Args) {
	name, err := s.attachTarget(a.ID)
	if err == nil {
		err = attachPTY(c, r, name, a.Cols, a.Rows)
	}
	if err != nil {
		// The error line is only meaningful before the switch to raw mode;
		// attachPTY only fails before it copies a byte.
		_ = json.NewEncoder(c).Encode(response{Err: err.Error(), Code: codeFor(err)})
		slog.Warn("ipc: attach", "id", a.ID, "err", err)
	}
}

// attachTarget revives the session and returns its tmux session name.
//
// EnsureTmux first, and the name read only afterwards: a parked session is
// exactly what a client most wants to attach to, and reviving one can
// migrate its tmux name (see App.EnsureTmux), so a name read before the call
// can be the old one.
func (s *Server) attachTarget(id string) (string, error) {
	if _, err := s.Backend.EnsureTmux(id); err != nil {
		return "", err
	}
	for _, sess := range s.Backend.Sessions() {
		if sess.ID == id {
			if sess.TmuxSession == "" {
				return "", fmt.Errorf("session %q has no tmux session", id)
			}
			return sess.TmuxSession, nil
		}
	}
	return "", fmt.Errorf("unknown session %q", id)
}

// attachPTY runs `tmux attach` on a fresh pty and copies both ways until
// either end hangs up. It writes the success response itself, so that line
// is the last thing on the connection that is JSON.
func attachPTY(c net.Conn, r io.Reader, session string, cols, rows int) error {
	cmd := tmux.AttachCmd(session)
	f, err := pty.StartWithSize(cmd, winsize(cols, rows))
	if err != nil {
		return fmt.Errorf("allocate pty: %w", err)
	}
	// pty.StartWithSize only Starts the child, so somebody has to Wait for
	// it or every attach leaves a zombie for the life of the core — and a
	// phone reattaching on each foreground makes that a steady drip, not a
	// one-off. Kill first: closing the master should hang the tmux client
	// up, but a client that ignores it would otherwise be waited on forever.
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := json.NewEncoder(c).Encode(response{Result: Result{OK: true}}); err != nil {
		_ = f.Close()
		return nil // the client is gone; there is nobody to report to
	}
	go func() {
		// Closing the socket is the detach: this copy ends, the pty's
		// master closes, tmux's client sees EOF and exits, and the copy
		// below returns. Nothing else has to notice.
		_, _ = io.Copy(f, r)
		_ = f.Close()
	}()
	// Ends on tmux exiting (the session was killed, or the user detached
	// from inside it) or on the pty closing above. Neither is an error
	// worth a log line — a detach reads as EIO on the master.
	_, _ = io.Copy(c, f)
	_ = f.Close()
	return nil
}

// winsize clamps a client's requested size into a pty.Winsize, defaulting
// anything nonsensical to 80x24 rather than to zero — a zero-sized pty makes
// tmux draw nothing at all, which reads as a hung connection.
func winsize(cols, rows int) *pty.Winsize {
	if cols <= 0 || cols > 0xffff {
		cols = 80
	}
	if rows <= 0 || rows > 0xffff {
		rows = 24
	}
	return &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}

// Attachment is a live pty over the wire: the client half of Attach. Read
// what the session draws, write keystrokes, Close to detach.
type Attachment struct {
	conn net.Conn
	r    io.Reader
}

func (a *Attachment) Read(p []byte) (int, error)  { return a.r.Read(p) }
func (a *Attachment) Write(p []byte) (int, error) { return a.conn.Write(p) }
func (a *Attachment) Close() error                { return a.conn.Close() }

// Attach revives id's tmux session and returns the pty behind it. Not part
// of tui.Backend: after the handshake this connection isn't the protocol any
// more, so there is nothing for the TUI-shaped interface to describe.
func (c *Client) Attach(id string, cols, rows int) (*Attachment, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(conn).Encode(request{
		Method: "Attach",
		Args:   Args{ID: id, Cols: cols, Rows: rows},
	}); err != nil {
		conn.Close()
		return nil, err
	}
	// Exactly one line, for the reason Server.handle reads one: a
	// json.Decoder would leave the response's terminating newline — and
	// however much of tmux's first screen draw arrived in the same packet —
	// stranded in a buffer the pty reader never sees. Everything after this
	// line is the pty, starting with the very next byte.
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	// A line without its terminator is a core that died mid-response, not a
	// response: reporting err beats letting json.Unmarshal blame the
	// truncated JSON ("unexpected end of JSON input") for the disconnect.
	if err != nil && !bytes.HasSuffix(line, []byte("\n")) {
		conn.Close()
		return nil, fmt.Errorf("attach %s: no response: %w", id, err)
	}
	var res response
	if err := json.Unmarshal(bytes.TrimSpace(line), &res); err != nil {
		conn.Close()
		return nil, err
	}
	if res.Err != "" {
		conn.Close()
		return nil, wireErr{msg: res.Err, sentinel: sentinels[res.Code]}
	}
	return &Attachment{conn: conn, r: br}, nil
}
