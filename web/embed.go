package web

//go:generate npm --prefix . ci
//go:generate npm --prefix . run build

import "embed"

//go:embed all:build
var BuildFS embed.FS
