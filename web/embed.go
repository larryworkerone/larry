// Package web embeds the dashboard assets so the server binary ships with a
// complete UI and needs no external static files or separate frontend build.
package web

import "embed"

// IndexHTML is the single dashboard page served at "/".
//
//go:embed index.html
var IndexHTML []byte

// Static is the embedded /static tree (app.js and future assets).
//
//go:embed static
var Static embed.FS
