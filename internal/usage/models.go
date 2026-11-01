package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Model is one model an agent offers, from agent-usage's models.json.
type Model struct {
	// ID is what the agent's --model flag takes.
	ID string `json:"id"`
	// Name is what the agent's own model picker shows ("Opus 5.5").
	Name string `json:"name"`
	// ThinkingLevels are the model's reasoning-effort levels, lowest first.
	// Empty for a model with no such setting.
	ThinkingLevels []string `json:"thinkingLevels"`
}

// ModelsPath is agent-usage's models.json, which sits next to usage.json.
func ModelsPath(home string) string {
	return filepath.Join(filepath.Dir(DefaultPath(home)), "models.json")
}

// ModelsReader re-reads models.json only when its mtime changes: it is asked
// for every snapshot, and agent-usage rewrites it about hourly. The zero
// value is ready to use, and it is safe for concurrent use.
type ModelsReader struct {
	mu     sync.Mutex
	path   string
	mtime  time.Time
	agents map[string][]Model
}

// Models returns the models agent-usage says agent offers at path, in the
// agent's own picker order, or nil when the file or the agent's entry is
// missing.
func (r *ModelsReader) Models(path, agent string) []Model {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := os.Stat(path)
	if err != nil {
		r.path, r.mtime, r.agents = "", time.Time{}, nil
		return nil
	}
	if path != r.path || !st.ModTime().Equal(r.mtime) {
		r.path, r.mtime, r.agents = path, st.ModTime(), readModels(path)
	}
	return r.agents[agent]
}

// readModels parses models.json into each agent's models, or nil when it
// can't be read or is a schema this core doesn't know.
func readModels(path string) map[string][]Model {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f struct {
		Schema int `json:"schema"`
		Agents []struct {
			Agent  string  `json:"agent"`
			Models []Model `json:"models"`
		} `json:"agents"`
	}
	if json.Unmarshal(b, &f) != nil || f.Schema != 1 {
		return nil
	}
	agents := map[string][]Model{}
	for _, a := range f.Agents {
		for _, m := range a.Models {
			if m.ID != "" {
				agents[a.Agent] = append(agents[a.Agent], m)
			}
		}
	}
	return agents
}
