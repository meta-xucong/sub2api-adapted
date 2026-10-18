//go:build !windows

package state

import (
	"os"
	"path/filepath"
)

func atomicReplace(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

func syncDirectory(path string) error {
	dir, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
