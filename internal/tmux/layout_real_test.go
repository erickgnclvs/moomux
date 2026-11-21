package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/layout"
)

// socketRunner drives a private tmux server on its own socket. $TMUX is
// emptied so a test run inside a managed session never reaches the user's
// real server (tmux prefers $TMUX over TMUX_TMPDIR).
type socketRunner struct{ sock string }

func (r socketRunner) Run(args ...string) (string, error) {
	// A detached session has a default size; fix it at creation so the
	// split percentages divide a known area (resizing afterwards rescales
	// the panes and hides rounding).
	if args[0] == "new-session" {
		args = append(append([]string(nil), args...), "-x", "100", "-y", "40")
	}
	cmd := exec.Command("tmux", append([]string{"-u", "-S", r.sock}, args...)...)
	cmd.Env = append(os.Environ(), "TMUX=")
	cmd.Dir = filepath.Dir(r.sock)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type paneGeom struct{ left, top, w, h int }

// realPanes builds windows in a real tmux server and returns the geometry of
// every pane in the first window, plus the index of the agent's pane.
func realPanes(t *testing.T, windows []layout.WindowSpec) (panes []paneGeom, agent int) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	// Socket paths over ~100 bytes fail, so keep this directory short.
	dir, err := os.MkdirTemp("/tmp", "mxl")
	if err != nil {
		t.Fatal(err)
	}
	r := socketRunner{sock: filepath.Join(dir, "s")}
	t.Cleanup(func() {
		// Stops this socket's own server only, never the default one.
		_, _ = r.Run("kill-server")
		_ = os.RemoveAll(dir)
	})
	c := &Client{Runner: r}
	if err := c.NewSessionWithLayout("moomux-x", dir, "x", windows, "true"); err != nil {
		t.Fatal(err)
	}
	out, err := r.Run("list-panes", "-t", "=moomux-x:^", "-F",
		"#{pane_left} #{pane_top} #{pane_width} #{pane_height} #{@moomux_agent_pane}")
	if err != nil {
		t.Fatal(err)
	}
	agent = -1
	for i, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		var g paneGeom
		for j, p := range []*int{&g.left, &g.top, &g.w, &g.h} {
			if *p, err = strconv.Atoi(f[j]); err != nil {
				t.Fatalf("bad pane line %q", line)
			}
		}
		if len(f) > 4 && f[4] == "1" {
			agent = i
		}
		panes = append(panes, g)
	}
	return panes, agent
}

func near(got, want int) bool { return got >= want-2 && got <= want+2 }

// TestRealTmuxSplitRowAgentRight runs the 70/30 split against a real server:
// the argv test can't tell whether `-b -p` carved the sizes it claims.
func TestRealTmuxSplitRowAgentRight(t *testing.T) {
	panes, agent := realPanes(t, []layout.WindowSpec{{PaneSpec: layout.PaneSpec{
		Direction: "row",
		Children: []layout.PaneSpec{
			{Size: "70%", Cmd: "true"},
			{Size: "30%", Agent: true},
		},
	}}})
	if len(panes) != 2 {
		t.Fatalf("panes = %+v, want 2", panes)
	}
	if agent < 0 || panes[agent].left == 0 {
		t.Fatalf("agent pane %d should be the right one: %+v", agent, panes)
	}
	// 100 columns minus one divider.
	if l, r := panes[1-agent], panes[agent]; !near(l.w, 69) || !near(r.w, 30) {
		t.Errorf("widths = %d / %d, want ~70 / ~30", l.w, r.w)
	}
}

// TestRealTmuxSplitColRescalesRemaining checks the rescaling in
// realizeLayout: 50/25/25 asks split-window for 50%, then 50% of the rest.
func TestRealTmuxSplitColRescalesRemaining(t *testing.T) {
	panes, agent := realPanes(t, []layout.WindowSpec{{PaneSpec: layout.PaneSpec{
		Direction: "col",
		Children: []layout.PaneSpec{
			{Size: "50%", Cmd: "true"},
			{Size: "25%", Cmd: "true"},
			{Size: "25%", Agent: true},
		},
	}}})
	if len(panes) != 3 {
		t.Fatalf("panes = %+v, want 3", panes)
	}
	if agent < 0 || panes[agent].top == 0 {
		t.Fatalf("agent pane should be the bottom one: agent=%d %+v", agent, panes)
	}
	// 40 rows minus two dividers: about 19 / 9 / 9.
	for _, p := range panes {
		if p.top == 0 && !near(p.h, 19) {
			t.Errorf("top pane height = %d, want ~19: %+v", p.h, panes)
		}
	}
	if !near(panes[agent].h, 9) {
		t.Errorf("agent pane height = %d, want ~9: %+v", panes[agent].h, panes)
	}
}
