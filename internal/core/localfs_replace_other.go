//go:build !windows

package core

import "os"

func replaceLocalFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
