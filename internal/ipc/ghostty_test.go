package ipc

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGhosttyConfigOrder: later files override earlier ones, so the load
// order is the contract — it must match ghostty's loadDefaultFiles and the
// Mac app's AppState.ghosttyConfigPaths, or the phone styles a pane with a
// setting the Mac app would have overridden.
func TestGhosttyConfigOrder(t *testing.T) {
	home := t.TempDir()
	dotConfig := filepath.Join(home, ".config", "ghostty")
	appSupport := filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty")
	xdgHome := t.TempDir()
	xdg := filepath.Join(xdgHome, "ghostty")
	// Written out of order, so a directory listing can't pass for load order.
	writeFile(t, filepath.Join(appSupport, "config.ghostty"), "a = 4")
	writeFile(t, filepath.Join(appSupport, "config"), "a = 3")
	writeFile(t, filepath.Join(dotConfig, "config.ghostty"), "a = 2")
	writeFile(t, filepath.Join(dotConfig, "config"), "a = 1")
	writeFile(t, filepath.Join(xdg, "config.ghostty"), "a = 6")
	writeFile(t, filepath.Join(xdg, "config"), "") // empty: skipped
	t.Setenv("HOME", home)

	for _, tc := range []struct {
		name, xdgHome string
		want          GhosttyConfig
	}{
		{"~/.config, then Application Support, legacy name first", "", GhosttyConfig{
			Text: "a = 1\na = 2\na = 3\na = 4\n",
			Files: []string{
				filepath.Join(dotConfig, "config"), filepath.Join(dotConfig, "config.ghostty"),
				filepath.Join(appSupport, "config"), filepath.Join(appSupport, "config.ghostty"),
			},
		}},
		{"XDG_CONFIG_HOME replaces ~/.config, and an empty file is skipped", xdgHome, GhosttyConfig{
			Text: "a = 6\na = 3\na = 4\n",
			Files: []string{
				filepath.Join(xdg, "config.ghostty"),
				filepath.Join(appSupport, "config"), filepath.Join(appSupport, "config.ghostty"),
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdgHome)
			if got := ghosttyConfig(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ghosttyConfig()\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// TestGhosttyConfigWireShape: no config anywhere is an answer, not an error —
// "ghostty" present with an empty "text", which the Swift client decodes by
// these exact keys. Pinned on the raw bytes, like TestDiffWireShape.
func TestGhosttyConfigWireShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	c, _ := start(t, &fakeBackend{}, &config.Config{}, nil)
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"method":"GhosttyConfig"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"result":{"ghostty":{"text":""}}}` + "\n"; line != want {
		t.Fatalf("GhosttyConfig response\n got: %s\nwant: %s", line, want)
	}
}
