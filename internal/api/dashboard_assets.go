package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dashboard
var dashboardFS embed.FS

func dashboardHandler() http.Handler {
	sub, err := fs.Sub(dashboardFS, "dashboard")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/dashboard/", http.FileServer(http.FS(sub)))
}
