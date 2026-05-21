package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenCodeWatcherBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"sess-1": "busy",
			"sess-2": "idle",
		})
	}))
	defer srv.Close()

	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: srv.URL}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond, Client: srv.Client()}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Working {
			t.Fatalf("expected Working, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestOpenCodeWatcherIdle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"sess-1": "idle",
		})
	}))
	defer srv.Close()

	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: srv.URL}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond, Client: srv.Client()}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Waiting {
			t.Fatalf("expected Waiting, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestOpenCodeWatcherUnreachable(t *testing.T) {
	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: "http://127.0.0.1:1"}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Waiting {
			t.Fatalf("expected Waiting when unreachable, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}
