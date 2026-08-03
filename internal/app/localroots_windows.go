//go:build windows

package app

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func logicalRoots() []localDirectory {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	result := make([]localDirectory, 0, 8)
	for index := uint(0); index < 26; index++ {
		if mask&(1<<index) == 0 {
			continue
		}
		letter := byte('A' + index)
		result = append(result, localDirectory{Name: fmt.Sprintf("%c 盘", letter), Path: fmt.Sprintf("%c:\\", letter)})
	}
	return result
}
