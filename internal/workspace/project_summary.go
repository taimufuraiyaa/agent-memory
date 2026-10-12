package workspace

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Discovery reads metadata without creating a database or running migrations.
func countProjectMemories(ctx context.Context, project Project) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := os.Stat(project.DBPath)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("project database is not a regular file")
	}
	path, err := filepath.Abs(project.DBPath)
	if err != nil {
		return 0, err
	}
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(250)"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	queryContext, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var count int
	err = db.QueryRowContext(queryContext, "SELECT COUNT(*) FROM memories WHERE workspace = ?", project.Name).Scan(&count)
	return count, err
}
