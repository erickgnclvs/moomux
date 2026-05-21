package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenCodeEntry maps one moomux worktree to its OpenCode HTTP server URL.
type OpenCodeEntry struct {
	WorktreePath string
	URL          string // e.g. "http://127.0.0.1:4096"
}

// OpenCodeWatcher polls each OpenCode session's HTTP API for status.
// Unreachable servers are treated as Waiting (OpenCode may still be starting up).
type OpenCodeWatcher struct {
	Entries  []OpenCodeEntry
	Interval time.Duration
	Client   *http.Client // nil = default client with short timeout
}

func (w *OpenCodeWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.tick(ctx, out, client)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out, client)
		}
	}
}

func (w *OpenCodeWatcher) tick(ctx context.Context, out chan<- Snapshot, client *http.Client) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	for _, e := range w.Entries {
		snap.States[e.WorktreePath] = pollOpenCode(client, e.URL)
	}
	send(ctx, out, snap)
}

func pollOpenCode(client *http.Client, baseURL string) State {
	resp, err := client.Get(fmt.Sprintf("%s/session/status", baseURL))
	if err != nil {
		return Waiting
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Waiting
	}
	var statuses map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return Waiting
	}
	for _, v := range statuses {
		if v == "busy" {
			return Working
		}
	}
	return Waiting
}
