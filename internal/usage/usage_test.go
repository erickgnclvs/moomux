package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC)

const sample = `{
  "schema": 2,
  "generatedAt": "2026-09-30T19:25:03Z",
  "agents": [
  { "agent": "codex", "quota": { "status": "ok", "windows": [{ "kind": "session", "percent": 99 }] } },
  { "agent": "claude", "quota": {
    "status": "ok",
    "updatedAt": "2026-09-30T19:24:44Z",
    "windows": [
      { "kind": "session", "percent": 40, "resetsAt": "2026-09-30T21:49:59Z" },
      { "kind": "weekly_all", "percent": 80, "resetsAt": "2026-10-02T13:59:59Z" },
      { "kind": "weekly_scoped", "model": "Fable", "percent": 95 },
      { "kind": "weekly_scoped", "percent": 79.9 },
      { "kind": "weekly_scoped", "percent": 79.4 },
      { "kind": "monthly_extra", "percent": 0 }
    ],
    "spend": { "x": 1 }
  },
  "tokens": {} }
  ]
}`

func read(t *testing.T, body string) *Usage {
	t.Helper()
	p := filepath.Join(t.TempDir(), "usage.json")
	if body != "" {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return (&Reader{Path: p}).Read(now)
}

func TestOmitted(t *testing.T) {
	for name, body := range map[string]string{
		"no file":   "",
		"bad json":  "{not json",
		"schema 1":  strings.Replace(sample, `"schema": 2`, `"schema": 1`, 1),
		"no agents": `{"schema": 2, "generatedAt": "2026-09-30T19:25:03Z", "agents": []}`,
		"no claude": strings.Replace(sample, `"agent": "claude"`, `"agent": "gemini"`, 1),
		"no quota":  `{"schema": 2, "generatedAt": "2026-09-30T19:25:03Z", "agents": [{"agent": "claude"}]}`,
		"idle": strings.Replace(sample, `"status": "ok",
    "updatedAt"`, `"status": "idle",
    "updatedAt"`, 1),
		"unknown status": strings.Replace(sample, `"status": "ok",
    "updatedAt"`, `"status": "rateLimited",
    "updatedAt"`, 1),
	} {
		if u := read(t, body); u != nil {
			t.Errorf("%s: got %+v, want omitted", name, u)
		}
	}
}

func TestWire(t *testing.T) {
	u := read(t, sample)
	if u == nil || u.Status != "ok" || u.UpdatedAt != "2026-09-30T19:24:44Z" {
		t.Fatalf("got %+v", u)
	}
	want := []Window{
		{Kind: "session", Name: "5h", Percent: 40, ResetsAt: "2026-09-30T21:49:59Z", Level: "ok", Headline: true},
		{Kind: "weekly_all", Name: "Week", Percent: 80, ResetsAt: "2026-10-02T13:59:59Z", Level: "warn", Headline: true},
		{Kind: "weekly_scoped", Name: "Fable", Percent: 95, Level: "critical"},
		{Kind: "weekly_scoped", Name: "Scoped", Percent: 80, Level: "warn"},
		{Kind: "weekly_scoped", Name: "Scoped", Percent: 79, Level: "ok"},
		{Kind: "monthly_extra", Name: "Monthly extra", Percent: 0, Level: "ok"},
	}
	if len(u.Windows) != len(want) {
		t.Fatalf("windows = %+v", u.Windows)
	}
	for i := range want {
		if u.Windows[i] != want[i] {
			t.Errorf("window %d = %+v, want %+v", i, u.Windows[i], want[i])
		}
	}
	b, _ := json.Marshal(u.Windows[2])
	if strings.Contains(string(b), "resets_at") {
		t.Errorf("absent resetsAt should be omitted: %s", b)
	}
}

func TestStale(t *testing.T) {
	old := strings.Replace(sample, "2026-09-30T19:25:03Z", "2026-09-30T19:14:59Z", 1)
	u := read(t, old)
	if u == nil || u.Status != "stale" || len(u.Windows) != 6 {
		t.Fatalf("got %+v, want stale with windows", u)
	}
	// 15m exactly is still fresh.
	if u := read(t, strings.Replace(sample, "2026-09-30T19:25:03Z", "2026-09-30T19:15:00Z", 1)); u.Status != "ok" {
		t.Errorf("at 15m: %q, want ok", u.Status)
	}
	// Stale is re-evaluated on an unchanged file.
	p := filepath.Join(t.TempDir(), "usage.json")
	os.WriteFile(p, []byte(sample), 0o644)
	r := &Reader{Path: p}
	if s := r.Read(now).Status; s != "ok" {
		t.Fatalf("first read %q", s)
	}
	if s := r.Read(now.Add(time.Hour)).Status; s != "stale" {
		t.Errorf("an hour later on the same file: %q, want stale", s)
	}
}

func TestSignedOut(t *testing.T) {
	u := read(t, `{"schema":2,"generatedAt":"2026-09-30T19:25:03Z","agents":[{"agent":"claude","quota":{"status":"signedOut","windows":[]}}]}`)
	if u == nil || u.Status != "signed_out" || u.Windows == nil || len(u.Windows) != 0 {
		t.Fatalf("got %+v", u)
	}
	b, _ := json.Marshal(u)
	if string(b) != `{"status":"signed_out","windows":[]}` {
		t.Errorf("wire = %s", b)
	}
}

func TestRereadsOnMtime(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	os.WriteFile(p, []byte(sample), 0o644)
	r := &Reader{Path: p}
	if r.Read(now) == nil {
		t.Fatal("first read nil")
	}
	os.WriteFile(p, []byte(strings.Replace(sample, `"status": "ok",
    "updatedAt"`, `"status": "failed",
    "updatedAt"`, 1)), 0o644)
	os.Chtimes(p, now, now.Add(time.Minute))
	if s := r.Read(now).Status; s != "failed" {
		t.Errorf("after rewrite: %q, want failed", s)
	}
	os.Remove(p)
	if u := r.Read(now); u != nil {
		t.Errorf("after delete: %+v, want omitted", u)
	}
}
