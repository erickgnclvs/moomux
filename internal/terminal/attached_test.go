package terminal

import (
	"reflect"
	"strings"
	"testing"
)

// The format template and the parser have to agree, and neither fails
// loudly when they don't — see the comment on clientFormat. This pins both
// halves against output shaped like real `tmux list-clients` output.
func TestParseClients(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want []tmuxClient
	}{
		{
			name: "one client",
			out:  "/dev/ttys011\t54321\n",
			want: []tmuxClient{{TTY: "/dev/ttys011", PID: "54321"}},
		},
		{
			name: "several clients, tmux's order preserved",
			out:  "/dev/ttys011\t1\n/dev/ttys022\t2\n",
			want: []tmuxClient{{TTY: "/dev/ttys011", PID: "1"}, {TTY: "/dev/ttys022", PID: "2"}},
		},
		{
			name: "nothing attached",
			out:  "",
			want: nil,
		},
		{
			name: "trailing blank lines are not clients",
			out:  "/dev/ttys011\t1\n\n\n",
			want: []tmuxClient{{TTY: "/dev/ttys011", PID: "1"}},
		},
		{
			// A tmux too old for one of the two fields prints it empty
			// rather than failing. The tty half still has to work — that's
			// what iTerm2 and wezterm join on.
			name: "missing pid field",
			out:  "/dev/ttys011\t\n",
			want: []tmuxClient{{TTY: "/dev/ttys011", PID: ""}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseClients(tc.out); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseClients(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

// The two fields the parser splits on must be the two the template asks
// for, in that order, separated by the tab it cuts on.
func TestClientFormatMatchesParser(t *testing.T) {
	if want := "#{client_tty}\t#{client_pid}"; clientFormat != want {
		t.Fatalf("clientFormat = %q, want %q", clientFormat, want)
	}
	if !strings.Contains(clientFormat, "\t") {
		t.Fatalf("clientFormat must separate its fields with the tab parseClients cuts on: %q", clientFormat)
	}
}
