package tui

import (
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func TestParkWorkingAgentNeedsSecondPress(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}}
	m := newTestModel(be)
	putView(m, "demo:a", sessionview.View{State: watcher.Working})

	run(m, keyRune("x"))
	if len(be.killCalls) != 0 || !strings.Contains(m.flash, "press x again") {
		t.Fatalf("first x: killCalls=%v flash=%q", be.killCalls, m.flash)
	}
	run(m, keyRune("x"))
	if len(be.killCalls) != 1 {
		t.Fatalf("second x: killCalls=%v", be.killCalls)
	}
}

func TestParkConfirmDisarmedByOtherKey(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}}
	m := newTestModel(be)
	putView(m, "demo:a", sessionview.View{State: watcher.Working})

	run(m, keyRune("x"))
	run(m, keyRune("k"))
	run(m, keyRune("x"))
	if len(be.killCalls) != 0 {
		t.Fatalf("x after another key parked a working agent: %v", be.killCalls)
	}
}

func TestOpenAndParkShowFeedbackBeforeResult(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "alpha"}}}
	m := newTestModel(be)
	m.Update(keyRune("o"))
	if !strings.Contains(m.flash, "opening alpha…") {
		t.Fatalf("flash after o = %q", m.flash)
	}
	m.Update(keyRune("x"))
	if !strings.Contains(m.flash, "parking alpha…") {
		t.Fatalf("flash after x = %q", m.flash)
	}
}

func TestConfirmDeleteSaysWhenGitCheckFailed(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}})
	m.width, m.height = 80, 30
	m.confirmID = "demo:a"
	m.confirmGit = gitStatusInfo{ok: false}
	m.confirmChecking = false
	m.mode = ModeConfirmDelete
	if got := m.View(); !strings.Contains(got, "couldn't check git status") {
		t.Fatalf("dialog hides the failed check:\n%s", got)
	}
}
