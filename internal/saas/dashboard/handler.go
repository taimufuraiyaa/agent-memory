// Package dashboard adapts the shared Agent Memory web application to the
// hosted /dashboard/ route.
package dashboard

import (
	"net/http"

	shareddashboard "github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
)

func Handler() http.Handler {
	return http.StripPrefix("/dashboard/", shareddashboard.GetEmbeddedHandler())
}
