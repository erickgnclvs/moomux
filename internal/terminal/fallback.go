package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// platformFallback is the opener for "no terminal recognized". On macOS
// something can still be launched (see commandFileOpener); elsewhere all
// that's left is telling the user how to attach.
func platformFallback() TerminalOpener {
	if runtime.GOOS == "darwin" {
		return &commandFileOpener{}
	}
	return &fallbackOpener{}
}

// fallbackOpener is used when no supported terminal is detected. It cannot
// open anything itself, so it hands the caller an attach instruction to
// display instead of writing to stdout directly — moomux runs its TUI on
// the alt screen, and writing straight to stdout there corrupts the display.
type fallbackOpener struct{}

func (f *fallbackOpener) OpenSession(tmuxSession, title string) (string, error) {
	// The "=" target is single-quoted: this line gets typed into an
	// interactive shell, and zsh's EQUALS expansion would otherwise read a
	// bare leading "=" as a command-path lookup and fail to find it.
	return fmt.Sprintf("no terminal detected, attach yourself: tmux attach -t '=%s'", tmuxSession), nil
}

// commandFileOpener hands the attach command to whatever app macOS has
// registered for .command files — Terminal.app by default, iTerm2 and
// friends when the user has changed it. It is the macOS fallback because
// Detect's env-var probes see nothing at all when the core runs as a
// launchd/brew service (`brew services start moomux`): the daemon has no
// TERM_PROGRAM and no $TMUX, so opening a session from any client of that
// core — `moomux ui -socket`, a native app — returned an attach hint and
// opened nothing at all.
type commandFileOpener struct{}

func (c *commandFileOpener) OpenSession(tmuxSession, title string) (string, error) {
	f, err := os.CreateTemp("", "moomux-attach-*.command")
	if err != nil {
		return new(fallbackOpener).OpenSession(tmuxSession, title)
	}
	// The script removes itself: `open` returns as soon as the app is
	// launched, so nothing here knows when the file has been read.
	// "=" pins tmux's -t to an exact match, same as windowOpener.
	_, err = fmt.Fprintf(f, "#!/bin/sh\nrm -f %s\nexec tmux attach -t %s\n",
		shellQuote(f.Name()), shellQuote("="+tmuxSession))
	f.Close()
	if err == nil {
		err = os.Chmod(f.Name(), 0o700)
	}
	if err == nil {
		err = exec.Command("open", f.Name()).Run()
	}
	if err != nil {
		os.Remove(f.Name())
		return new(fallbackOpener).OpenSession(tmuxSession, title)
	}
	return "", nil
}
