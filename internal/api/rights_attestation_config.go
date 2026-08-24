package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
)

// ConfigureLocalRightsAttestation enables the control-plane boundary for the
// single-installation product. Its stable local subject is a development
// identity, not a replacement for authenticated SaaS request context.
func ConfigureLocalRightsAttestation(ctx context.Context, svc *Service) error {
	if svc == nil {
		return errors.New("API service is required")
	}
	baseDir := strings.TrimSpace(svc.BaseDir)
	if baseDir == "" {
		return errors.New("base directory is required for rights attestation")
	}
	absoluteBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	store, err := attestation.OpenSQLiteStore(ctx, filepath.Join(absoluteBaseDir, "agent-memory-control.db"))
	if err != nil {
		return err
	}
	svc.RightsAttestationStore = store
	svc.RightsAttestation = attestation.NewService(store)
	localSubjectID := libraryID("local-account", filepath.Clean(absoluteBaseDir))
	svc.RightsSubjectResolver = func(*http.Request) (string, error) {
		return localSubjectID, nil
	}
	return nil
}
