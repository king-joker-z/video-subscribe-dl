package web

import "embed"

//go:embed all:static
var staticFS embed.FS

//go:embed all:templates
var templateFS embed.FS
