package usage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestModels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "models.json")
	if got := (&ModelsReader{}).Models(p, "claude"); got != nil {
		t.Fatalf("missing file: Models = %v, want nil", got)
	}
	body := `{"schema":1,"generatedAt":"2026-09-30T17:05:00Z","agents":[
	  {"agent":"claude","models":[
	    {"id":"claude-opus-5-5","name":"Opus 5.5","thinkingLevels":["low","high","max"]},
	    {"id":"claude-haiku-4-5","name":"Haiku 4.5"}]}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []Model{{ID: "claude-opus-5-5", Name: "Opus 5.5", ThinkingLevels: []string{"low", "high", "max"}}, {ID: "claude-haiku-4-5", Name: "Haiku 4.5"}}
	if got := (&ModelsReader{}).Models(p, "claude"); !reflect.DeepEqual(got, want) {
		t.Fatalf("claude: Models = %+v, want %+v", got, want)
	}
	if got := (&ModelsReader{}).Models(p, "codex"); got != nil {
		t.Fatalf("codex absent: Models = %v, want nil", got)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(body, `"schema":1`, `"schema":2`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (&ModelsReader{}).Models(p, "claude"); got != nil {
		t.Fatalf("unknown schema: Models = %v, want nil", got)
	}
}

// The reader keeps its parse until the file changes, then picks up the
// rewrite: agent-usage replaces models.json under a running core.
func TestModelsReaderRereadsOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "models.json")
	write := func(id string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(p, []byte(`{"schema":1,"agents":[{"agent":"codex","models":[{"id":"`+id+`"}]}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	var r ModelsReader
	start := time.Now().Add(-time.Hour)
	write("gpt-a", start)
	if got := r.Models(p, "codex"); len(got) != 1 || got[0].ID != "gpt-a" {
		t.Fatalf("first read = %+v", got)
	}
	write("gpt-b", start.Add(time.Minute))
	if got := r.Models(p, "codex"); len(got) != 1 || got[0].ID != "gpt-b" {
		t.Fatalf("after rewrite = %+v, want gpt-b", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := r.Models(p, "codex"); got != nil {
		t.Fatalf("after delete = %+v, want nil", got)
	}
}
