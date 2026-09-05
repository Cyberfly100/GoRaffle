// Package web embeds the frontend assets (templates + static files) so the
// compiled binary is fully self-contained.
package web

import "embed"

//go:embed templates static
var FS embed.FS
