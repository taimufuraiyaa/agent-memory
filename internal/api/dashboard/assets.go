// Package dashboard provides embedded dashboard assets and serving utilities.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var embeddedAssets embed.FS

// HasEmbeddedAssets returns true if dashboard assets are embedded in the binary.
func HasEmbeddedAssets() bool {
	// Check if the dist directory exists in embedded FS
	_, err := embeddedAssets.ReadDir("dist")
	return err == nil
}

// GetEmbeddedFS returns the embedded dashboard assets as a filesystem.
// The returned filesystem is rooted at the dist directory.
func GetEmbeddedFS() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "dist")
}

// GetEmbeddedHandler returns an HTTP handler that serves embedded dashboard assets.
// If assets are not available, returns a handler that returns 404.
func GetEmbeddedHandler() http.Handler {
	fsys, err := GetEmbeddedFS()
	if err != nil {
		// Return error handler
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard assets not available", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Asset names are intentionally stable (app.js/app.css), so browsers must
		// revalidate after the single binary is replaced.
		w.Header().Set("Cache-Control", "no-store")
		fileServer.ServeHTTP(w, r)
	})
}
