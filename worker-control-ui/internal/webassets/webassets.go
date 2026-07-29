// Package webassets embeds the static control-panel frontend so the
// worker-control-ui binary is a single self-contained executable (matching
// the runtime image's "just the compiled binary" scratch/distroless base).
package webassets

import "embed"

//go:embed web
var FS embed.FS
