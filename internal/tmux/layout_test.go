package tmux

import (
	"reflect"
	"testing"

	"github.com/erickgnclvs/moomux/internal/layout"
)

func TestNewSessionWithLayoutAgentOnRight(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-panes -t =moomux-x:^ -F #{pane_id}":                    "%3\n",
		"split-window -h -b -t %3 -c /tmp/wt -p 70 -P -F #{pane_id}": "%4\n",
	}}
	c := &Client{Runner: fr}
	windows := []layout.WindowSpec{{PaneSpec: layout.PaneSpec{
		Direction: "row",
		Children: []layout.PaneSpec{
			{Size: "70%", Cmd: "npm run dev"},
			{Size: "30%", Agent: true},
		},
	}}}
	if err := c.NewSessionWithLayout("moomux-x", "/tmp/wt", "x", windows, "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-x", "-c", "/tmp/wt", "-n", "x"},
		{"set-window-option", "-t", "=moomux-x:^", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-x:^", "set-titles", "on"},
		{"set-option", "-t", "=moomux-x:^", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-x:^", "mouse", "on"},
		{"list-panes", "-t", "=moomux-x:^", "-F", "#{pane_id}"},
		{"split-window", "-h", "-b", "-t", "%3", "-c", "/tmp/wt", "-p", "70", "-P", "-F", "#{pane_id}"},
		{"send-keys", "-t", "%4", "npm run dev", "Enter"},
		{"select-pane", "-t", "%3"},
		{"set-window-option", "-t", "%3", "@moomux_agent", "1"},
		{"display-message", "-p", "-t", "%3", "#{window_id}"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionWithLayoutNestedGrid(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-panes -t =moomux-x:^ -F #{pane_id}":                    "%3\n",
		"split-window -h -b -t %3 -c /tmp/wt -p 50 -P -F #{pane_id}": "%4\n",
		"split-window -v -b -t %4 -c /tmp/wt -p 50 -P -F #{pane_id}": "%5\n",
	}}
	c := &Client{Runner: fr}
	windows := []layout.WindowSpec{{PaneSpec: layout.PaneSpec{
		Direction: "row",
		Children: []layout.PaneSpec{
			{
				Direction: "col",
				Size:      "50%",
				Children: []layout.PaneSpec{
					{Cmd: "top-left"},
					{Agent: true},
				},
			},
			{Size: "50%", Cmd: "right-pane"},
		},
	}}}
	if err := c.NewSessionWithLayout("moomux-x", "/tmp/wt", "x", windows, "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-x", "-c", "/tmp/wt", "-n", "x"},
		{"set-window-option", "-t", "=moomux-x:^", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-x:^", "set-titles", "on"},
		{"set-option", "-t", "=moomux-x:^", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-x:^", "mouse", "on"},
		{"list-panes", "-t", "=moomux-x:^", "-F", "#{pane_id}"},
		{"split-window", "-h", "-b", "-t", "%3", "-c", "/tmp/wt", "-p", "50", "-P", "-F", "#{pane_id}"},
		{"split-window", "-v", "-b", "-t", "%4", "-c", "/tmp/wt", "-p", "50", "-P", "-F", "#{pane_id}"},
		{"send-keys", "-t", "%5", "top-left", "Enter"},
		{"send-keys", "-t", "%3", "right-pane", "Enter"},
		{"select-pane", "-t", "%4"},
		{"set-window-option", "-t", "%4", "@moomux_agent", "1"},
		{"display-message", "-p", "-t", "%4", "#{window_id}"},
		{"send-keys", "-t", "%4", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionWithLayoutMultipleWindows(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-panes -t =moomux-x:^ -F #{pane_id}":                     "%3\n",
		"new-window -t =moomux-x -c /tmp/wt -n logs -P -F #{pane_id}": "%9\n",
		"split-window -v -b -t %9 -c /tmp/wt -p 50 -P -F #{pane_id}":  "%10\n",
	}}
	c := &Client{Runner: fr}
	windows := []layout.WindowSpec{
		{PaneSpec: layout.PaneSpec{Agent: true}},
		{Name: "logs", PaneSpec: layout.PaneSpec{
			Direction: "col",
			Children: []layout.PaneSpec{
				{Cmd: "tail -f logs/dev.log"},
				{Cmd: "docker compose logs -f"},
			},
		}},
	}
	if err := c.NewSessionWithLayout("moomux-x", "/tmp/wt", "x", windows, "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-x", "-c", "/tmp/wt", "-n", "x"},
		{"set-window-option", "-t", "=moomux-x:^", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-x:^", "set-titles", "on"},
		{"set-option", "-t", "=moomux-x:^", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-x:^", "mouse", "on"},
		{"list-panes", "-t", "=moomux-x:^", "-F", "#{pane_id}"},
		{"new-window", "-t", "=moomux-x", "-c", "/tmp/wt", "-n", "logs", "-P", "-F", "#{pane_id}"},
		{"set-window-option", "-t", "%9", "automatic-rename", "off"},
		{"set-option", "-t", "%9", "set-titles", "on"},
		{"set-option", "-t", "%9", "set-titles-string", "#{window_name}"},
		{"split-window", "-v", "-b", "-t", "%9", "-c", "/tmp/wt", "-p", "50", "-P", "-F", "#{pane_id}"},
		{"send-keys", "-t", "%10", "tail -f logs/dev.log", "Enter"},
		{"send-keys", "-t", "%9", "docker compose logs -f", "Enter"},
		{"select-window", "-t", "%3"},
		{"select-pane", "-t", "%3"},
		{"set-window-option", "-t", "%3", "@moomux_agent", "1"},
		{"display-message", "-p", "-t", "%3", "#{window_id}"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// TestNewSessionWithLayoutNameFollowsAgentWindow verifies that the session's
// display name is given to whichever window holds the agent even when that
// window isn't the first one listed, rather than always landing on window 0.
func TestNewSessionWithLayoutNameFollowsAgentWindow(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-panes -t =moomux-x:^ -F #{pane_id}":                    "%3\n",
		"split-window -h -b -t %3 -c /tmp/wt -p 50 -P -F #{pane_id}": "%4\n",
		"new-window -t =moomux-x -c /tmp/wt -n x -P -F #{pane_id}":   "%7\n",
	}}
	c := &Client{Runner: fr}
	windows := []layout.WindowSpec{
		{PaneSpec: layout.PaneSpec{
			Direction: "row",
			Children: []layout.PaneSpec{
				{Cmd: "shell1"},
				{Cmd: "shell2"},
			},
		}},
		{PaneSpec: layout.PaneSpec{Agent: true}},
	}
	if err := c.NewSessionWithLayout("moomux-x", "/tmp/wt", "x", windows, "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-x", "-c", "/tmp/wt"},
		{"set-option", "-t", "=moomux-x:^", "mouse", "on"},
		{"list-panes", "-t", "=moomux-x:^", "-F", "#{pane_id}"},
		{"split-window", "-h", "-b", "-t", "%3", "-c", "/tmp/wt", "-p", "50", "-P", "-F", "#{pane_id}"},
		{"send-keys", "-t", "%4", "shell1", "Enter"},
		{"send-keys", "-t", "%3", "shell2", "Enter"},
		{"new-window", "-t", "=moomux-x", "-c", "/tmp/wt", "-n", "x", "-P", "-F", "#{pane_id}"},
		{"set-window-option", "-t", "%7", "automatic-rename", "off"},
		{"set-option", "-t", "%7", "set-titles", "on"},
		{"set-option", "-t", "%7", "set-titles-string", "#{window_name}"},
		{"select-window", "-t", "%7"},
		{"select-pane", "-t", "%7"},
		{"set-window-option", "-t", "%7", "@moomux_agent", "1"},
		{"display-message", "-p", "-t", "%7", "#{window_id}"},
		{"send-keys", "-t", "%7", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// TestNewSessionWithLayoutAgentWindowNameIgnoresFileName verifies that a
// Name set in the file on the agent's own window is overridden by moomux's
// session display name rather than being honored.
func TestNewSessionWithLayoutAgentWindowNameIgnoresFileName(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-panes -t =moomux-x:^ -F #{pane_id}": "%3\n",
	}}
	c := &Client{Runner: fr}
	windows := []layout.WindowSpec{
		{Name: "ignored", PaneSpec: layout.PaneSpec{Agent: true}},
	}
	if err := c.NewSessionWithLayout("moomux-x", "/tmp/wt", "x", windows, "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-x", "-c", "/tmp/wt", "-n", "x"},
		{"set-window-option", "-t", "=moomux-x:^", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-x:^", "set-titles", "on"},
		{"set-option", "-t", "=moomux-x:^", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-x:^", "mouse", "on"},
		{"list-panes", "-t", "=moomux-x:^", "-F", "#{pane_id}"},
		{"select-pane", "-t", "%3"},
		{"set-window-option", "-t", "%3", "@moomux_agent", "1"},
		{"display-message", "-p", "-t", "%3", "#{window_id}"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}
