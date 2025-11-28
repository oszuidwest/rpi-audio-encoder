package main

import "embed"

//go:embed web/index.html
var indexHTML string

//go:embed web/style.css
var styleCSS string

//go:embed web/app.js
var appJS string

// WebFiles contains all embedded web assets for direct filesystem access.
//
//go:embed web/*
var WebFiles embed.FS
