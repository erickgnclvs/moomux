// Package usage serves Claude quota usage, read from the usage.json that
// agent-usage (github.com/afitzgerald/agent-usage) writes every five minutes.
// The core only ever reads that file. The shared contract with the Mac and
// iPhone apps is moomux-mac's docs/usage-contract.md; docs/wire-protocol.md
// has the wire half.
package usage

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleAfter is how old generatedAt may get before the status becomes
// "stale": three missed launchd runs, so the job is not running.
const StaleAfter = 15 * time.Minute

// Usage is Snapshot.Usage on the wire.
type Usage struct {
	Status    string   `json:"status"` // ok | failed | signed_out | stale
	UpdatedAt string   `json:"updated_at,omitempty"`
	Windows   []Window `json:"windows"` // never null
}

type Window struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Percent  int    `json:"percent"` // used, not remaining
	ResetsAt string `json:"resets_at,omitempty"`
	Level    string `json:"level"` // ok | warn | critical
	Headline bool   `json:"headline"`
}

// file is the slice of usage.json (schema 3) the core reads. Dates stay
// strings so updated_at and resets_at go out verbatim.
type file struct {
	Schema      int       `json:"schema"`
	GeneratedAt time.Time `json:"generatedAt"`
	// One entry per agent, keyed by "agent". Only Claude's is served; the
	// rest are some other reader's.
	Agents []struct {
		Agent string `json:"agent"`
		Quota *quota `json:"quota"`
	} `json:"agents"`
}

type quota struct {
	Status    string `json:"status"`
	UpdatedAt string `json:"updatedAt"`
	Windows   []struct {
		Kind     string  `json:"kind"`
		Model    string  `json:"model"`
		Percent  float64 `json:"percentUsed"`
		ResetsAt string  `json:"resetsAt"`
	} `json:"windows"`
}

// claude is Claude's quota, by key rather than position; nil when the file
// has no Claude entry or that entry has no quota yet.
func (f *file) claude() *quota {
	for _, a := range f.Agents {
		if a.Agent == "claude" {
			return a.Quota
		}
	}
	return nil
}

// DefaultPath is agent-usage's output, or $MOOMUX_USAGE_FILE when set.
func DefaultPath(home string) string {
	if p := os.Getenv("MOOMUX_USAGE_FILE"); p != "" {
		return p
	}
	return filepath.Join(home, "Library", "Application Support", "AgentUsage", "usage.json")
}

// Reader re-reads Path when its mtime changes. Not safe for concurrent use;
// sessionview's run loop owns it.
type Reader struct {
	Path string

	mtime time.Time
	f     *file // nil: missing or undecodable
}

// Why usage is omitted: Snapshot.UsageSetup on the wire. Clients decode
// these verbatim.
const (
	NotInstalled = "not_installed" // no file: agent-usage not installed or never ran
	Unreadable   = "unreadable"    // the file can't be read or isn't JSON
	Unsupported  = "unsupported"   // a schema this core doesn't read
	NoClaude     = "no_claude"     // no Claude entry or quota yet, or idle
)

// Read returns the usage to serve at now, or nil and the reason (one of the
// constants above) when there is none to show. It stats the file every call
// but decodes only on an mtime change; stale is re-evaluated every call,
// since a job that stopped writing is exactly the case where the mtime never
// moves.
func (r *Reader) Read(now time.Time) (*Usage, string) {
	st, err := os.Stat(r.Path)
	if err != nil {
		r.mtime, r.f = time.Time{}, nil
		if os.IsNotExist(err) {
			return nil, NotInstalled
		}
		return nil, Unreadable
	}
	if !st.ModTime().Equal(r.mtime) {
		r.mtime, r.f = st.ModTime(), nil
		if b, err := os.ReadFile(r.Path); err == nil {
			var f file
			if json.Unmarshal(b, &f) == nil {
				r.f = &f
			}
		}
	}
	return build(r.f, now)
}

func build(f *file, now time.Time) (*Usage, string) {
	if f == nil {
		return nil, Unreadable
	}
	if f.Schema != 3 {
		return nil, Unsupported
	}
	q := f.claude()
	if q == nil {
		return nil, NoClaude
	}
	u := &Usage{UpdatedAt: q.UpdatedAt, Windows: []Window{}}
	switch q.Status {
	case "ok", "failed":
		u.Status = q.Status
	case "signedOut":
		u.Status = "signed_out"
	default: // idle, or a status this core doesn't know
		return nil, NoClaude
	}
	// A missing generatedAt can't prove the file fresh, so it reads as stale.
	if now.Sub(f.GeneratedAt) > StaleAfter {
		u.Status = "stale"
	}
	if u.Status == "signed_out" {
		return u, ""
	}
	for _, w := range q.Windows {
		// Rounded half-up, and level taken from the rounded value, so the
		// number shown and its colour can never disagree (79.9 is 80, warn).
		pct := int(math.Floor(w.Percent + 0.5))
		u.Windows = append(u.Windows, Window{
			Kind:     w.Kind,
			Name:     name(w.Kind, w.Model),
			Percent:  pct,
			ResetsAt: w.ResetsAt,
			Level:    level(pct),
			Headline: w.Kind == "session" || w.Kind == "weekly_all",
		})
	}
	return u, ""
}

func name(kind, model string) string {
	switch kind {
	case "session":
		return "5h"
	case "weekly_all":
		return "Week"
	case "weekly_scoped":
		if model != "" {
			return model
		}
		return "Scoped"
	}
	// An unknown kind still gets a row rather than vanishing.
	s := strings.ReplaceAll(kind, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// level is the only place the thresholds live; clients colour by it.
func level(percent int) string {
	switch {
	case percent >= 95:
		return "critical"
	case percent >= 80:
		return "warn"
	}
	return "ok"
}
