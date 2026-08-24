package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	api "github.com/taimufuraiyaa/agent-memory/internal/saas/api"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type localProjectService struct {
	manager  *workspace.Manager
	modelDir string
}

func newLocalProjectService() (*localProjectService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	baseDir := strings.TrimSpace(os.Getenv("AGENT_MEMORY_LOCAL_PROJECTS_DIR"))
	if baseDir == "" {
		baseDir = filepath.Join(home, ".agent-memory")
	}
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		return nil, err
	}
	modelDir := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_DIR"))
	if modelDir == "" {
		modelDir = embeddings.DefaultModelDir(home)
	}
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return nil, err
	}
	return &localProjectService{manager: manager, modelDir: modelDir}, nil
}

func (service *localProjectService) List(ctx context.Context) ([]workspace.ListItem, error) {
	return service.manager.List(ctx)
}

func (service *localProjectService) Study(ctx context.Context, input api.LocalProjectStudyInput) (*engine.StudyResult, error) {
	project, err := service.manager.Project(input.Workspace)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(project.WorkspaceRoot) == "" {
		return nil, errors.New("project has no registered root; re-register it before studying")
	}
	provider, err := embeddings.NewLocalProvider(service.modelDir)
	if err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, project.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	writer := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{Embedder: provider})
	return engine.NewStudyEngine(writer).IngestWithOptions(ctx, engine.StudyOptions{
		Workspace: input.Workspace,
		Sources:   workspace.DefaultStudySources(project.WorkspaceRoot),
		Depth:     input.Depth,
		DryRun:    input.DryRun,
		MaxFiles:  input.MaxFiles,
		Offset:    input.Offset,
	})
}

func (service *localProjectService) ListFeedback(ctx context.Context, workspaceName string) ([]core.RetrievalRequestLog, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListRetrievalRequests(ctx, workspaceName)
}

func (service *localProjectService) RecordFeedback(ctx context.Context, input api.LocalProjectFeedbackInput) error {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return err
	}
	defer store.Close()
	useful, total := -1, -1
	if input.UsefulCount != nil {
		useful = *input.UsefulCount
	}
	if input.TotalCount != nil {
		total = *input.TotalCount
	}
	return store.RecordRequestFeedback(ctx, input.RequestID, input.Score, input.Reason, useful, total)
}

func (service *localProjectService) Search(ctx context.Context, input api.LocalProjectSearchInput) ([]api.LocalProjectMemoryResult, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	provider, err := embeddings.NewLocalProvider(service.modelDir)
	if err != nil {
		return nil, err
	}
	topK := input.Offset + input.Limit
	hits, err := engine.NewVectorSearcher(store, provider).Search(ctx, input.Workspace, input.Query, topK)
	if err != nil {
		return nil, err
	}
	if input.Offset >= len(hits) {
		return []api.LocalProjectMemoryResult{}, nil
	}
	hits = hits[input.Offset:]
	if len(hits) > input.Limit {
		hits = hits[:input.Limit]
	}
	results := make([]api.LocalProjectMemoryResult, 0, len(hits))
	for _, hit := range hits {
		results = append(results, api.LocalProjectMemoryResult{Memory: hit.Memory, Score: hit.Score, Explanation: "semantic similarity"})
	}
	return results, nil
}

func (service *localProjectService) Browse(ctx context.Context, input api.LocalProjectBrowseInput) ([]core.MemoryEntry, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	memories, err := store.ListRecentMemoriesByWorkspace(ctx, input.Workspace, input.Offset+input.Limit+1)
	if err != nil {
		return nil, err
	}
	if input.Mode == "pinned" {
		filtered := memories[:0]
		for _, memory := range memories {
			if memory.Pinned {
				filtered = append(filtered, memory)
			}
		}
		memories = filtered
	}
	if input.Offset >= len(memories) {
		return []core.MemoryEntry{}, nil
	}
	memories = memories[input.Offset:]
	if len(memories) > input.Limit {
		memories = memories[:input.Limit]
	}
	return memories, nil
}

func (service *localProjectService) GetMemory(ctx context.Context, workspaceName, memoryID string) (*core.MemoryEntry, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	memory, err := store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	if memory.Workspace != workspaceName {
		return nil, errors.New("memory is outside the requested workspace")
	}
	return memory, nil
}

func (service *localProjectService) ListSolutionEpisodes(ctx context.Context, workspaceName string, limit int) ([]application.SolutionActivityEpisode, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).ListActivityEpisodes(ctx, workspaceName, limit)
}

func (service *localProjectService) GetSolutionEpisode(ctx context.Context, workspaceName, episodeID string) (application.SolutionActivityDetail, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return application.SolutionActivityDetail{}, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).GetActivityEpisode(ctx, workspaceName, episodeID)
}

func (service *localProjectService) ReviewSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionReviewInput) error {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return err
	}
	defer store.Close()
	episode, err := store.GetSolutionEpisode(ctx, input.EpisodeID)
	if err != nil || episode.Workspace != input.Workspace {
		return errors.New("solution episode is outside the registered project")
	}
	solutions := application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	switch input.Action {
	case "pin":
		return solutions.SetEpisodePinned(ctx, application.SolutionEpisodePinInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, Pinned: input.Pinned})
	case "misleading":
		return solutions.MarkStepMisleading(ctx, application.SolutionStepReviewInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, StepID: input.StepID, Reason: input.Reason})
	case "redact":
		return solutions.RedactStep(ctx, application.SolutionStepRedactInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, StepID: input.StepID, ReasonClass: input.ReasonClass})
	case "correct":
		_, err = solutions.CorrectSummary(ctx, application.SolutionSummaryCorrectionInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, Summary: input.Summary, IdempotencyKey: input.IdempotencyKey})
		return err
	case "supersede":
		return solutions.SupersedeEpisode(ctx, application.SolutionEpisodeSupersedeInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, SuccessorEpisodeID: input.SuccessorEpisodeID})
	case "delete":
		return solutions.DeleteEpisode(ctx, application.SolutionEpisodeDeleteInput{Workspace: input.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID, Reason: input.Reason})
	default:
		return errors.New("invalid solution review action")
	}
}

func (service *localProjectService) StartSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionStartInput) (core.SolutionEpisode, bool, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Start(ctx, application.SolutionStartInput{
		Workspace: input.Workspace, SessionID: input.SessionID, PrincipalID: input.PrincipalID, ClientID: input.ClientID,
		GoalSummary: input.GoalSummary, CapturePolicy: core.SolutionCapturePolicy(input.CapturePolicy),
		RetentionClass: core.SolutionRetentionClass(input.RetentionClass), IdempotencyKey: input.IdempotencyKey, Origin: engine.SolutionOriginHuman,
	})
}

func (service *localProjectService) AppendSolutionStep(ctx context.Context, input api.LocalProjectSolutionStepInput) (core.SolutionStep, bool, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).AppendStep(ctx, application.SolutionAppendStepInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID,
		Kind: core.SolutionStepKind(input.Kind), Status: core.SolutionStepStatus(input.Status), Summary: input.Summary,
		RationaleSummary: input.RationaleSummary, Source: input.Source, Confidence: input.Confidence,
		Sensitivity: core.SolutionSensitivity(input.Sensitivity), IdempotencyKey: input.IdempotencyKey, Origin: engine.SolutionOriginHuman,
	})
}

func (service *localProjectService) CheckpointSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionCheckpointInput) (core.SolutionWorkingState, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Checkpoint(ctx, application.SolutionCheckpointInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID, ExpectedGeneration: input.ExpectedGeneration,
		GoalSummary: input.GoalSummary, Constraints: input.Constraints, CompletedItems: input.CompletedItems, OpenQuestions: input.OpenQuestions,
		NextAction: input.NextAction, Sensitivity: core.SolutionSensitivity(input.Sensitivity), TTL: time.Duration(input.TTLSeconds) * time.Second, Origin: engine.SolutionOriginHuman,
	})
}

func (service *localProjectService) TransitionSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionTransitionInput) (core.SolutionEpisode, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionEpisode{}, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Transition(ctx, application.SolutionTransitionInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID, ExpectedVersion: input.ExpectedVersion,
		Status: core.SolutionEpisodeStatus(input.Status), IdempotencyKey: input.IdempotencyKey,
	})
}

func (service *localProjectService) HandoffSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionHandoffInput) (core.SolutionEpisode, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionEpisode{}, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Handoff(ctx, application.SolutionHandoffInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID, ExpectedVersion: input.ExpectedVersion,
		TargetPrincipalID: input.TargetPrincipalID, TargetSessionID: input.TargetSessionID, IdempotencyKey: input.IdempotencyKey,
	})
}

func (service *localProjectService) FinalizeSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionFinalizeInput) (core.SolutionSummary, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	defer store.Close()
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Finalize(ctx, application.SolutionFinalizeInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID,
		ExpectedVersion: input.ExpectedVersion, IdempotencyKey: input.IdempotencyKey,
	})
}

func (service *localProjectService) RecallSolutionPaths(ctx context.Context, input api.LocalProjectSolutionRecallInput) (engine.HowRecallResult, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return engine.HowRecallResult{}, err
	}
	defer store.Close()
	return engine.NewHowRecallService(store).Recall(ctx, engine.HowRecallInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, SessionID: input.SessionID, Task: input.Task,
		TokenBudget: input.TokenBudget, MaxCandidates: input.MaxCandidates,
	})
}

func (service *localProjectService) ExportSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionExportInput) (api.LocalProjectSolutionExport, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return api.LocalProjectSolutionExport{}, err
	}
	defer store.Close()
	solutions := application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	detail, err := solutions.GetActivityEpisode(ctx, input.Workspace, input.EpisodeID)
	if err != nil || detail.Episode.PrincipalID != input.PrincipalID {
		return api.LocalProjectSolutionExport{}, errors.New("solution episode is outside the authorized principal")
	}
	result := api.LocalProjectSolutionExport{Detail: detail}
	if !detail.Episode.Status.Terminal() {
		state, stateErr := solutions.GetWorkingState(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
		if stateErr == nil {
			result.WorkingState = &state
		}
	}
	return result, nil
}

func (service *localProjectService) openProjectStore(ctx context.Context, workspaceName string) (*sqlite.Store, error) {
	project, err := service.manager.Project(workspaceName)
	if err != nil {
		return nil, err
	}
	return sqlite.Open(ctx, project.DBPath)
}
