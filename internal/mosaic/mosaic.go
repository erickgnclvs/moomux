package mosaic

import (
	"fmt"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

const windowName = "moomux-mosaic"

type Client struct {
	Tmux *tmux.Client
}

func (c *Client) Open(sessions []session.Session) error {
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions to tile")
	}

	_ = c.Tmux.KillWindow(windowName)

	if err := c.Tmux.NewWindow(windowName); err != nil {
		return fmt.Errorf("new-window: %w", err)
	}

	if err := c.Tmux.SendKeys(windowName, "tmux attach -t "+sessions[0].TmuxSession); err != nil {
		return fmt.Errorf("send-keys pane 0: %w", err)
	}

	for _, s := range sessions[1:] {
		if err := c.Tmux.SplitWindow(windowName); err != nil {
			return fmt.Errorf("split-window for %s: %w", s.Name, err)
		}
		if err := c.Tmux.SendKeys(windowName, "tmux attach -t "+s.TmuxSession); err != nil {
			return fmt.Errorf("send-keys for %s: %w", s.Name, err)
		}
	}

	if err := c.Tmux.SelectLayout(windowName, "tiled"); err != nil {
		return fmt.Errorf("select-layout: %w", err)
	}

	_ = c.Tmux.SetPaneBorderStatus(windowName, "top")

	for i, s := range sessions {
		_ = c.Tmux.SelectPane(fmt.Sprintf("%s.%d", windowName, i), s.Name)
	}

	return c.Tmux.SelectWindow(windowName)
}
