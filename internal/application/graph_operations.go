package application

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrGraphOperationConflict = contracts.ErrGraphOperationConflict
	ErrGraphOperationNotFound = contracts.ErrGraphOperationNotFound
	ErrGraphOperationDisabled = contracts.ErrGraphOperationDisabled
	ErrGraphOperationInvalid  = contracts.ErrGraphOperationInvalid
)

type GraphOperationAction = contracts.GraphOperationAction

const (
	GraphOperationEnable   = contracts.GraphOperationEnable
	GraphOperationUpdate   = contracts.GraphOperationUpdate
	GraphOperationRebuild  = contracts.GraphOperationRebuild
	GraphOperationCancel   = contracts.GraphOperationCancel
	GraphOperationRetry    = contracts.GraphOperationRetry
	GraphOperationDisable  = contracts.GraphOperationDisable
	GraphOperationRollback = contracts.GraphOperationRollback
)

type GraphOperationRequest = contracts.GraphOperationRequest
type GraphIndexReadiness = contracts.GraphIndexReadiness
type GraphIndexStatus = contracts.GraphIndexStatus
type GraphOperationResult = contracts.GraphOperationResult
type GraphOperationController = contracts.GraphOperationController
type GraphOperationStore = contracts.GraphOperationStore

type GraphOperationService struct {
	store   GraphOperationStore
	runtime *config.GraphConfig
}

func NewGraphOperationService(store GraphOperationStore) *GraphOperationService {
	return &GraphOperationService{store: store}
}

func NewLocalGraphOperationService(store GraphOperationStore, configuration config.GraphConfig) *GraphOperationService {
	return &GraphOperationService{store: store, runtime: &configuration}
}

func (s *GraphOperationService) Readiness(ctx context.Context, scope core.GraphScope, configurationID string) (GraphIndexReadiness, error) {
	if err := validateGraphOperationIdentity(scope, configurationID); err != nil {
		return GraphIndexReadiness{}, err
	}
	readiness, err := s.store.GraphIndexReadiness(ctx, scope, strings.TrimSpace(configurationID))
	if err != nil || !readiness.Ready || s.runtime == nil {
		return readiness, err
	}
	if !s.runtime.Enabled {
		readiness.Ready = false
		readiness.State = "disabled"
		readiness.ReasonCode = "runtime_disabled"
		readiness.Reason = "Enable local graph runtime and start agent-memory serve"
		return readiness, nil
	}
	result, runErr := NewLocalGraphRunner(*s.runtime).Run(ctx, LocalGraphReadiness, nil)
	if result.JobDir != "" {
		defer os.RemoveAll(result.JobDir)
	}
	if runErr != nil || result.State != "ready" {
		readiness.Ready = false
		readiness.State = "unavailable"
		readiness.ReasonCode = "adapter_unavailable"
		readiness.Reason = "Configured local adapter did not pass readiness"
	}
	return readiness, nil
}

func (s *GraphOperationService) Status(ctx context.Context, scope core.GraphScope, configurationID string) (GraphIndexStatus, error) {
	if err := validateGraphOperationIdentity(scope, configurationID); err != nil {
		return GraphIndexStatus{}, err
	}
	return s.store.GraphIndexStatus(ctx, scope, strings.TrimSpace(configurationID))
}

func (s *GraphOperationService) Operate(ctx context.Context, request GraphOperationRequest) (GraphOperationResult, error) {
	if err := validateGraphOperationIdentity(request.Scope, request.ConfigurationID); err != nil {
		return GraphOperationResult{}, err
	}
	switch request.Action {
	case GraphOperationUpdate, GraphOperationRebuild:
		if strings.TrimSpace(request.IdempotencyKey) == "" {
			return GraphOperationResult{}, fmt.Errorf("%w: idempotency_key is required", ErrGraphOperationInvalid)
		}
	case GraphOperationCancel, GraphOperationRetry:
		if strings.TrimSpace(request.JobID) == "" {
			return GraphOperationResult{}, fmt.Errorf("%w: job_id is required", ErrGraphOperationInvalid)
		}
	case GraphOperationEnable, GraphOperationDisable, GraphOperationRollback:
	default:
		return GraphOperationResult{}, fmt.Errorf("%w: unsupported action %q", ErrGraphOperationInvalid, request.Action)
	}
	request.ConfigurationID = strings.TrimSpace(request.ConfigurationID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.ExpectedRevision = strings.TrimSpace(request.ExpectedRevision)
	request.JobID = strings.TrimSpace(request.JobID)
	if request.Actor == "" {
		request.Actor = "system"
	}
	return s.store.ApplyGraphOperation(ctx, request)
}

func validateGraphOperationIdentity(scope core.GraphScope, configurationID string) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrGraphOperationInvalid, err)
	}
	if strings.TrimSpace(configurationID) == "" {
		return fmt.Errorf("%w: configuration_id is required", ErrGraphOperationInvalid)
	}
	return nil
}
