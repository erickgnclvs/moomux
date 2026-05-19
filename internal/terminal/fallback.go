package terminal

import (
	"fmt"
	"io"
	"os"
)

type fallbackOpener struct {
	out io.Writer
}

func (f *fallbackOpener) OpenSession(tmuxSession, title string) error {
	w := f.out
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, "curral: run the following to attach to your session:\n  tmux attach -t %s\n", tmuxSession)
	return nil
}
