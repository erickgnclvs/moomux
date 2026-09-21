package tmux

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/erickgnclvs/moomux/internal/layout"
)

// NewSessionWithLayout creates a detached tmux session at cwd with one
// window per entry in windows, each arranged per its own pane tree instead
// of NewSession's fixed two-pane split. Whichever window contains the
// Agent: true leaf always gets windowName as its tmux name, overriding any
// Name it set in the file — moomux's own session naming wins for the
// agent's window regardless of its position in windows — and every other
// window uses its own Name, falling back to tmux's numbering when unnamed.
// agentCmd is sent into the agent leaf (layout.Load guarantees exactly one
// exists across all windows).
func (c *Client) NewSessionWithLayout(name, cwd, windowName string, windows []layout.WindowSpec, agentCmd string) error {
	if len(windows) == 0 {
		return fmt.Errorf("tmux: NewSessionWithLayout requires at least one window")
	}
	agentWindow := 0
	for i := range windows {
		if windows[i].HasAgent() {
			agentWindow = i
			break
		}
	}
	windowNameFor := func(i int) string {
		if i == agentWindow {
			return windowName
		}
		return windows[i].Name
	}

	first := windows[0]
	if err := c.newSessionBase(name, cwd, windowNameFor(0)); err != nil {
		return err
	}
	rootPane, err := c.Runner.Run("list-panes", "-t", firstWindow(name), "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	agentPane, err := c.realizeLayout(cwd, strings.TrimSpace(rootPane), &first.PaneSpec)
	if err != nil {
		return err
	}

	for i, w := range windows[1:] {
		wName := windowNameFor(i + 1)
		args := []string{"new-window", "-t", Exact(name), "-c", cwd}
		if wName != "" {
			args = append(args, "-n", wName)
		}
		args = append(args, "-P", "-F", "#{pane_id}")
		out, err := c.Runner.Run(args...)
		if err != nil {
			return err
		}
		winPane := strings.TrimSpace(out)
		if wName != "" {
			// Same window-name stabilization newSessionBase applies to the
			// first window — without it tmux overwrites the name with the
			// running shell's process name once it starts.
			_, _ = c.Runner.Run("set-window-option", "-t", winPane, "automatic-rename", "off")
			_, _ = c.Runner.Run("set-option", "-t", winPane, "set-titles", "on")
			_, _ = c.Runner.Run("set-option", "-t", winPane, "set-titles-string", "#{window_name}")
		}
		found, err := c.realizeLayout(cwd, winPane, &w.PaneSpec)
		if err != nil {
			return err
		}
		if found != "" {
			agentPane = found
		}
	}

	if len(windows) > 1 {
		// new-window switched the session's displayed window to whatever it
		// created last; if the agent pane lives in an earlier window,
		// select-pane alone (which only sets the active pane *within* its
		// own window) would leave that other window on screen instead.
		if _, err := c.Runner.Run("select-window", "-t", agentPane); err != nil {
			return err
		}
	}
	if _, err := c.Runner.Run("select-pane", "-t", agentPane); err != nil {
		return err
	}
	// Which window that pane is in is not recoverable later — the window's
	// name is the agent-state title, which moomux rewrites — so record it
	// now; everything targeting "the agent's window" reads it back.
	c.markAgentWindow(name, agentPane)
	if agentCmd != "" {
		if _, err := c.Runner.Run("send-keys", "-t", agentPane, agentCmd, "Enter"); err != nil {
			return err
		}
	}
	return nil
}

// realizeLayout carves paneID's screen space according to spec and returns
// the pane_id of the leaf marked Agent: true.
//
// For a split node, children are peeled off one at a time in order: each
// iteration splits paneID itself, inserting the new pane *before* it (-b) so
// paneID keeps representing "everything not yet carved" while the new pane
// takes the current child's slot — that alone produces correct left-to-right
// (or top-to-bottom) ordering with no separate bookkeeping. A child's Size is
// relative to its parent's whole space, but split-window's -p is relative to
// whatever's left in paneID at that point, so the requested percentage is
// rescaled against the sum of the not-yet-carved children's sizes.
func (c *Client) realizeLayout(cwd, paneID string, spec *layout.PaneSpec) (string, error) {
	if len(spec.Children) == 0 {
		if spec.Agent {
			return paneID, nil
		}
		if spec.Cmd != "" {
			if _, err := c.Runner.Run("send-keys", "-t", paneID, spec.Cmd, "Enter"); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	splitFlag := "-h" // "row": side-by-side columns
	if spec.Direction == "col" {
		splitFlag = "-v" // stacked rows
	}

	sizes := make([]float64, len(spec.Children))
	for i, child := range spec.Children {
		if child.Size == "" {
			sizes[i] = 100.0 / float64(len(spec.Children))
			continue
		}
		v, err := layout.ParsePercent(child.Size)
		if err != nil {
			return "", err
		}
		sizes[i] = v
	}

	agentPane := ""
	for i := 0; i < len(spec.Children)-1; i++ {
		remaining := 0.0
		for _, s := range sizes[i:] {
			remaining += s
		}
		pct := strconv.Itoa(int(sizes[i]/remaining*100 + 0.5))
		out, err := c.Runner.Run("split-window", splitFlag, "-b", "-t", paneID, "-c", cwd, "-p", pct, "-P", "-F", "#{pane_id}")
		if err != nil {
			return "", err
		}
		newPane := strings.TrimSpace(out)
		found, err := c.realizeLayout(cwd, newPane, &spec.Children[i])
		if err != nil {
			return "", err
		}
		if found != "" {
			agentPane = found
		}
	}
	found, err := c.realizeLayout(cwd, paneID, &spec.Children[len(spec.Children)-1])
	if err != nil {
		return "", err
	}
	if found != "" {
		agentPane = found
	}
	return agentPane, nil
}
