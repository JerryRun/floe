//go:build !windows

package app

func logicalRoots() []localDirectory {
	return []localDirectory{{Name: "根目录", Path: "/"}}
}
