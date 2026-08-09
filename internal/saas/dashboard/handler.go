// Package dashboard serves the hosted source-custody user interface.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

func Handler() http.Handler {
	contents, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(contents))
	return http.StripPrefix("/dashboard/", files)
}
