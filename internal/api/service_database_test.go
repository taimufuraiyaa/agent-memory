package api

import (
	"context"
	"path/filepath"
	"testing"
)

func TestServiceFixedWorkspaceUsesExactDBPath(t *testing.T) {
	exact := filepath.Join(t.TempDir(), "custom-name.db")
	service := &Service{Workspace: "workspace-name", BaseDir: filepath.Dir(exact), DBPath: exact}
	defer func() { _ = service.Close() }()
	assets, err := service.resolve(context.Background(), "workspace-name")
	if err != nil {
		t.Fatal(err)
	}
	if assets.DBPath != exact {
		t.Fatalf("resolved %q, want exact database %q", assets.DBPath, exact)
	}
}
