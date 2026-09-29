package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// clipboardImage saves an image on the system clipboard to a temp PNG and
// returns its path, or an error when there is no image (or no way to read
// one). Ctrl+V in the prompt field calls it before falling back to the
// textarea's own text paste — the same trick Claude Code uses, since a
// terminal paste (Cmd+V) only ever carries text.
//
// It reads *this* process's clipboard, so it's front-end work (see the
// client/core split in AGENTS.md), and over SSH it sees the host's
// clipboard, not the viewer's — which in practice means no image, and a
// plain text paste.
//
// A var so tests can stub it.
//
// ponytail: macOS only (osascript); add wl-paste/xclip when someone runs
// the TUI on a Linux desktop.
var clipboardImage = func() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", os.ErrNotExist
	}
	f, err := os.CreateTemp("", "moomux-paste-*.png")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	script := `set f to open for access (POSIX file "` + path + `") with write permission
try
	write (the clipboard as «class PNGf») to f
	close access f
on error e
	close access f
	error e
end try`
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// pasteClipboardImage inserts a clipboard image's path at the prompt's
// cursor, reporting whether there was one. Agents read an image path in a
// prompt as the image.
func (m *Model) pasteClipboardImage() bool {
	path, err := clipboardImage()
	if err != nil {
		return false
	}
	// Space-padded so the path never fuses with neighbouring text.
	sep := " "
	if v := m.promptInput.Value(); v == "" || strings.HasSuffix(v, " ") || strings.HasSuffix(v, "\n") {
		sep = ""
	}
	m.promptInput.InsertString(sep + path + " ")
	return true
}
