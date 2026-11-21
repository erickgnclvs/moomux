// Package atomicfile writes a file via a temp file + rename, so a reader
// never observes a partially-written file and a crash mid-write never
// truncates the previous contents.
package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes an exclusive advisory lock on path+".lock" and returns the
// release func. It serialises read-modify-write cycles across processes
// (the in-process mutexes only cover one). The lock lives on a sidecar file
// because Write replaces path itself by rename, which would orphan a lock
// held on the old inode.
func Lock(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil // closing drops the flock
}

// Write replaces path's contents with data, creating path's parent
// directory if it's missing, and chmods the result to mode.
//
// The temp file is created in path's own directory (rename is only atomic
// within a filesystem) with a unique per-call name: a fixed ".tmp" name
// races when several moomux processes write the same file at once, where
// one process's rename can steal or delete another's in-flight temp. It's
// also dot-prefixed so a leftover temp from a crash stays out of the way of
// anything listing the directory.
//
// A symlinked path is written through: the target is replaced and the link
// stays, so a dotfile managed by a symlink farm is not turned into a copy.
func Write(path string, data []byte, mode fs.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
