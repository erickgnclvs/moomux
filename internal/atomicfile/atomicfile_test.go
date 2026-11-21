package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real", "settings.json")
	link := filepath.Join(dir, "settings.json")
	if err := Write(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was replaced by a regular file: %v %v", fi, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q, want new", got)
	}
}

func TestWriteDanglingSymlinkIsReplaced(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "f")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(link); string(got) != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, err := Lock(path) // separate open file description: blocks like another process
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("second Lock returned while the first was held")
	default:
	}
	unlock()
	<-got
}
