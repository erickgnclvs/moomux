package codexhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAgentExecutableInstallsAndRefreshesNeutralCopy(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "moomux")
	if err := os.WriteFile(source, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureAgentExecutable(home, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first install should report changed")
	}
	destination := AgentExecutablePath(home)
	if filepath.Base(destination) == "moomux" {
		t.Fatalf("agent executable must use a neutral RTK-safe name: %s", destination)
	}
	assertExecutableContents(t, destination, "first")

	changed, err = EnsureAgentExecutable(home, source)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("identical executable should not be rewritten")
	}

	if err := os.WriteFile(source, []byte("second"), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err = EnsureAgentExecutable(home, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("updated executable should report changed")
	}
	assertExecutableContents(t, destination, "second")
}

func assertExecutableContents(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != want {
		t.Fatalf("contents = %q, want %q", body, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("agent executable is not executable: mode %o", info.Mode().Perm())
	}
}
