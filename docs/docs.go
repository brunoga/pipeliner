// Package docs embeds the pipeliner user guide for serving from the web UI.
package docs

import "embed"

//go:embed user-guide.html images
var FS embed.FS
