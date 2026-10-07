package frontend

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var distFS embed.FS

// Assets serves the root of dist/ to Wails WebView2.
var Assets, _ = fs.Sub(distFS, "dist")
