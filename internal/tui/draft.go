package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
)

// newSessionDraftPath is where the new-session form keeps the first prompt
// until a create succeeds. A prompt can be paragraphs of work, and every
// way off the form other than success — Esc, ctrl+c, a failed create
// followed by Esc, the TUI dying mid-create, an SSH drop — used to throw it
// away. It sits beside client.toml: it is this front end's state, not the
// core's. A var so TestMain can turn it off for the package (tests share one
// XDG dir, and a draft left by one test would prefill the next one's form).
var newSessionDraftPath = func() string {
	return filepath.Join(filepath.Dir(config.ClientPath()), "new-session-draft.txt")
}

// saveNewSessionDraft writes the form's prompt to the draft file, or
// removes the file when the prompt is blank.
func (m *Model) saveNewSessionDraft() {
	if m.draftPath == "" {
		return
	}
	prompt := m.promptInput.Value()
	if strings.TrimSpace(prompt) == "" {
		m.clearNewSessionDraft()
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.draftPath), 0o700); err != nil {
		slog.Warn("save new-session draft", "path", m.draftPath, "err", err)
		return
	}
	if err := os.WriteFile(m.draftPath, []byte(prompt), 0o600); err != nil {
		slog.Warn("save new-session draft", "path", m.draftPath, "err", err)
	}
}

func (m *Model) clearNewSessionDraft() {
	if m.draftPath == "" {
		return
	}
	if err := os.Remove(m.draftPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("clear new-session draft", "path", m.draftPath, "err", err)
	}
}

// clearNewSessionDraftOf clears the draft only if it is still prompt. Creates
// overlap, so by the time one lands the draft may be another form's.
func (m *Model) clearNewSessionDraftOf(prompt string) {
	if b, err := os.ReadFile(m.draftPath); err == nil && string(b) == prompt {
		m.clearNewSessionDraft()
	}
}

// loadNewSessionDraft returns the saved prompt, or "" when there is none.
//
// A draft that a session has already consumed is dropped. The draft is saved
// before the create runs, and SessionCreatedMsg is what clears it, so a TUI
// that dies after the core finished (an SSH drop, a quit mid-create) leaves
// the prompt of a session that exists. Reopening the form would then show
// that prior session's first prompt as if it were unsent.
func (m *Model) loadNewSessionDraft() string {
	if m.draftPath == "" {
		return ""
	}
	b, err := os.ReadFile(m.draftPath)
	if err != nil {
		return ""
	}
	draft := string(b)
	if info, err := os.Stat(m.draftPath); err == nil && m.draftConsumed(draft, info.ModTime()) {
		m.clearNewSessionDraft()
		return ""
	}
	return draft
}

// draftConsumed reports whether a session created since saved carries draft
// in its stored first prompt. The stored prompt is the composed one (thinking
// prefix, ticket and PR lines), so it contains the draft rather than equal it.
func (m *Model) draftConsumed(draft string, saved time.Time) bool {
	draft = strings.TrimSpace(draft)
	for _, s := range m.allSessions() {
		if !s.CreatedAt.Before(saved) && strings.Contains(s.Prompt, draft) {
			return true
		}
	}
	return false
}
