package main

import _ "embed"

// indexHTML contains the embedded HTML content for the main web interface.
//
//go:embed web/index.html
var indexHTML string

// loginHTML contains the embedded HTML content for the login page.
//
//go:embed web/login.html
var loginHTML string

// styleCSS contains the embedded CSS styles for the web interface.
//
//go:embed web/style.css
var styleCSS string

// appJS contains the embedded JavaScript application code.
//
//go:embed web/app.js
var appJS string

// iconsJS contains the embedded SVG icons as JavaScript.
//
//go:embed web/icons.js
var iconsJS string

// alpineJS contains the embedded Alpine.js library.
//
//go:embed web/alpine.min.js
var alpineJS string
