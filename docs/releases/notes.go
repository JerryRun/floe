// Package releases holds the release notes distributed with Floe.
package releases

import "embed"

//go:embed v*.md
var files embed.FS

// Notes returns the bundled release notes for tag, or an empty string.
func Notes(tag string) string {
	data, _ := files.ReadFile(tag + ".md")
	return string(data)
}
