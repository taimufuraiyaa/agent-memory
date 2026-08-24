package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
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
	clients  *clientprofile.Store
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
	clients, err := clientprofile.Open(baseDir, time.Now)
	if err != nil {
		return nil, err
	}
	return &localProjectService{manager: manager, modelDir: modelDir, clients: clients}, nil
}

func (service *localProjectService) Lifecycle(ctx context.Context, workspaceName string, limit int) (api.LocalProjectLifecycle, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return api.LocalProjectLifecycle{}, err
	}
	defer store.Close()
	runs, err := store.ListSchedulerRunHistory(ctx, workspaceName, limit)
	if err != nil {
		return api.LocalProjectLifecycle{}, err
	}
	history := make([]api.LocalProjectLifecycleRun, 0, len(runs))
	for _, run := range runs {
		history = append(history, api.LocalProjectLifecycleRun{ID: run.ID, Workspace: run.Workspace, StartedAt: run.StartedAt, CompletedAt: run.CompletedAt, Trigger: run.Trigger, Result: run.Result, SkipReason: run.SkipReason, DurationMS: run.DurationMS, DecayUpdated: run.DecayUpdated, Consolidated: run.Consolidated, ConflictsFound: run.ConflictsFound, Evicted: run.Evicted, Promoted: run.Promoted, Demoted: run.Demoted, Error: run.Error})
	}
	return api.LocalProjectLifecycle{History: history}, nil
}

func (service *localProjectService) Skills(_ context.Context, workspaceName string) ([]api.LocalProjectSkill, error) {
	project, err := service.manager.Project(workspaceName)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(project.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve registered project root: %w", err)
	}
	skillsDir, err := filepath.EvalSymlinks(filepath.Join(root, ".agents", "skills"))
	if os.IsNotExist(err) {
		return []api.LocalProjectSkill{}, nil
	}
	if err != nil || !pathWithinRoot(root, skillsDir) {
		return nil, errors.New("workspace skills directory is unavailable")
	}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, err
	}
	skills := make([]api.LocalProjectSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 12_000 {
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil || !pathWithinRoot(root, resolved) {
			continue
		}
		content, readErr := os.ReadFile(resolved)
		if readErr != nil {
			continue
		}
		displayName, description, body := parseLocalSkill(string(content))
		if displayName == "" {
			displayName = entry.Name()
		}
		skills = append(skills, api.LocalProjectSkill{Name: entry.Name(), DisplayName: displayName, Description: description, Content: body, Path: filepath.ToSlash(filepath.Join(".agents", "skills", entry.Name(), "SKILL.md"))})
	}
	return skills, nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func parseLocalSkill(raw string) (name, description, content string) {
	content = raw
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return "", "", content
	}
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		return "", "", content
	}
	content = strings.TrimSpace(parts[2])
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`)
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), `"'`)
		}
	}
	return name, description, content
}

func (service *localProjectService) ListClientProfiles(context.Context) ([]clientprofile.Profile, error) {
	return service.clients.List(), nil
}

func (service *localProjectService) CreateClientProfile(_ context.Context, input clientprofile.Input) (clientprofile.Profile, error) {
	return service.clients.Create(input)
}

func (service *localProjectService) UpdateClientProfile(_ context.Context, id string, expectedRevision int64, input clientprofile.Input) (clientprofile.Profile, error) {
	return service.clients.Update(id, expectedRevision, input)
}

func (service *localProjectService) DeleteClientProfile(_ context.Context, id string, expectedRevision int64) error {
	return service.clients.Delete(id, expectedRevision)
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
	fetchLimit := input.Offset + input.Limit + 1
	if input.Mode == "ungrouped" {
		fetchLimit = 200
	}
	memories, err := store.ListRecentMemoriesByWorkspace(ctx, input.Workspace, fetchLimit)
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
	if input.Mode == "ungrouped" {
		grouped, groupErr := store.ListPublishedSolutionPromotionMemoryIDs(ctx, input.Workspace)
		if groupErr != nil {
			return nil, groupErr
		}
		filtered := memories[:0]
		for _, memory := range memories {
			if _, linked := grouped[memory.ID]; !linked {
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

func (service *localProjectService) PromoteSolutionEpisode(ctx context.Context, input api.LocalProjectSolutionPromoteInput) (application.SolutionPromotionResult, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return application.SolutionPromotionResult{}, err
	}
	defer store.Close()
	provider, err := embeddings.NewLocalProvider(service.modelDir)
	if err != nil {
		return application.SolutionPromotionResult{}, err
	}
	writer := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{Embedder: provider})
	targets := make([]application.SolutionPromotionTarget, 0, len(input.Targets))
	for _, target := range input.Targets {
		targets = append(targets, application.SolutionPromotionTarget{MemoryType: core.MemoryType(target.MemoryType), Content: target.Content, SourceStepIDs: target.SourceStepIDs})
	}
	return application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy(), application.WithSolutionWriter(writer)).Promote(ctx, application.SolutionPromoteInput{
		Workspace: input.Workspace, PrincipalID: input.PrincipalID, EpisodeID: input.EpisodeID, SummaryID: input.SummaryID,
		IdempotencyKey: input.IdempotencyKey, Targets: targets,
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
