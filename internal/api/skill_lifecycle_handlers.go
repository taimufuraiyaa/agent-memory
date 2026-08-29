package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type SkillMutationAuthorizer interface {
	AuthorizeSkillMutation(context.Context, string, string, string, string) error
}

type skillLifecycleRequest struct {
	Operation string          `json:"operation"`
	Workspace string          `json:"workspace"`
	Actor     string          `json:"actor"`
	Payload   json.RawMessage `json:"payload"`
}
type skillProposeRequest struct {
	CandidateID       string                  `json:"candidate_id"`
	SkillName         string                  `json:"skill_name"`
	Description       string                  `json:"description"`
	OwnerGroup        string                  `json:"owner_group"`
	Files             map[string]string       `json:"files"`
	RemovalReasons    map[string]string       `json:"removal_reasons"`
	Compatibility     core.SkillCompatibility `json:"compatibility"`
	ProtectedSections []string                `json:"protected_sections"`
}
type skillDisableRequest struct {
	RevisionID    string                  `json:"revision_id"`
	ExpectedState core.SkillRevisionState `json:"expected_state"`
}

func skillListHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		if ws == "" {
			ws = svc.Workspace
		}
		items, err := assets.Store.ListLogicalSkills(r.Context(), ws, parseSkillLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, 200, map[string]any{"skills": items})
	}
}

func skillInspectHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		ws, skillID := strings.TrimSpace(r.URL.Query().Get("workspace")), strings.TrimSpace(r.URL.Query().Get("skill_id"))
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		if ws == "" {
			ws = svc.Workspace
		}
		skill, err := assets.Store.GetLogicalSkill(r.Context(), ws, skillID)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		revisions, err := assets.Store.ListSkillRevisions(r.Context(), ws, skill.ID, 200)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		evaluations, err := assets.Store.ListSkillEvaluationRuns(r.Context(), ws, skill.ID, 200)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		decisions, err := assets.Store.ListSkillPolicyDecisions(r.Context(), ws, skill.ID, 200)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		result := map[string]any{"skill": skill, "revisions": revisions, "evaluations": evaluations, "policy_decisions": decisions}
		environment := r.URL.Query().Get("environment")
		if environment == "" {
			environment = "local"
		}
		if activation, activationErr := assets.Store.GetSkillActivation(r.Context(), ws, environment, skill.ID); activationErr == nil {
			result["activation"] = activation
		} else if !errors.Is(activationErr, sql.ErrNoRows) {
			writeSolutionError(w, activationErr)
			return
		}
		writeOK(w, 200, result)
	}
}

func skillLifecycleHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var request skillLifecycleRequest
		if !decodeSolutionRequest(w, r, &request) {
			return
		}
		request.Operation = strings.TrimSpace(request.Operation)
		request.Actor = strings.TrimSpace(request.Actor)
		assets, err := svc.resolve(r.Context(), request.Workspace)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		ws := request.Workspace
		if ws == "" {
			ws = svc.Workspace
		}
		if err := authorizeSkillMutation(r.Context(), svc, request.Actor, ws, request.Operation, skillMutationTarget(request.Payload)); err != nil {
			writeErr(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		var result any
		switch request.Operation {
		case "propose":
			var input skillProposeRequest
			if err = json.Unmarshal(request.Payload, &input); err != nil {
				break
			}
			candidate, loadErr := assets.Store.GetSkillCandidate(r.Context(), ws, input.CandidateID)
			if loadErr != nil {
				err = loadErr
				break
			}
			if candidate.CreatedBy != request.Actor {
				err = errors.New("candidate author does not match actor")
				break
			}
			root, rootErr := skillProjectRoot(svc, ws)
			if rootErr != nil {
				err = rootErr
				break
			}
			bundles, bundleErr := workspace.NewRevisionBundleStore(root)
			if bundleErr != nil {
				err = bundleErr
				break
			}
			files := map[string][]byte{}
			for name, content := range input.Files {
				files[name] = []byte(content)
			}
			result, err = application.NewSkillRevisionBuilder(assets.Store, bundles).Build(r.Context(), application.SkillRevisionBuildInput{Workspace: ws, CandidateID: input.CandidateID, SkillName: input.SkillName, Description: input.Description, OwnerGroup: input.OwnerGroup, CreatedBy: request.Actor, ProposedFiles: files, RemovalReasons: input.RemovalReasons, Compatibility: input.Compatibility, ProtectedSections: input.ProtectedSections})
		case "evaluate":
			var input application.SkillEvaluationInput
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				if svc.SkillEvaluationRunner == nil {
					err = application.ErrSkillEvaluatorUnavailable
				} else {
					result, err = application.NewSkillEvaluationOrchestrator(assets.Store, svc.SkillEvaluationRunner, time.Now).Evaluate(r.Context(), input)
				}
			}
		case "approve":
			var input application.SkillApprovalInput
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				input.ApproverID = request.Actor
				if svc.SkillApprovalAuthorizer == nil {
					err = errors.New("skill approval authorizer is required")
				} else {
					result, err = application.NewSkillApprovalService(assets.Store, svc.SkillApprovalAuthorizer, time.Now).Approve(r.Context(), input)
				}
			}
		case "canary":
			var input application.SkillCanaryAllocationInput
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				result = application.SkillCanaryAllocator{}.Allocate(input)
			}
		case "promote", "rollback":
			var input application.SkillActivationRequest
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				input.Actor = request.Actor
				if request.Operation == "rollback" {
					input.Rollback = true
					input.Automatic = false
				}
				root, rootErr := skillProjectRoot(svc, ws)
				if rootErr != nil {
					err = rootErr
				} else {
					bundles, bundleErr := workspace.NewRevisionBundleStore(root)
					if bundleErr != nil {
						err = bundleErr
					} else {
						materializer, materialErr := workspace.NewSkillMaterializer(root, bundles)
						if materialErr != nil {
							err = materialErr
						} else {
							result, err = application.NewSkillActivationService(assets.Store, materializer, time.Now).Activate(r.Context(), input)
						}
					}
				}
			}
		case "resolve", "pin":
			var input application.SkillResolutionRequest
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				if svc.SkillResolutionAuthorizer == nil {
					err = errors.New("skill resolution authorizer is required")
				} else {
					root, rootErr := skillProjectRoot(svc, ws)
					if rootErr != nil {
						err = rootErr
					} else {
						bundles, bundleErr := workspace.NewRevisionBundleStore(root)
						if bundleErr != nil {
							err = bundleErr
						} else {
							materializer, materialErr := workspace.NewSkillMaterializer(root, bundles)
							if materialErr != nil {
								err = materialErr
							} else {
								verifier, _ := workspace.NewSkillArtifactVerifier(bundles, materializer)
								result, err = application.NewSkillResolver(assets.Store, svc.SkillResolutionAuthorizer, verifier, time.Now).Resolve(r.Context(), input)
							}
						}
					}
				}
			}
		case "acknowledge":
			var input application.SkillAcknowledgementInput
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				result, err = application.NewSkillAcknowledgementService(assets.Store, time.Now).Acknowledge(r.Context(), input)
			}
		case "complete":
			var input application.SkillExecutionInput
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				input.Workspace = ws
				result, err = application.NewSkillExecutionService(assets.Store).Complete(r.Context(), input)
			}
		case "disable":
			var input skillDisableRequest
			err = json.Unmarshal(request.Payload, &input)
			if err == nil {
				revision, getErr := assets.Store.GetSkillRevision(r.Context(), ws, input.RevisionID)
				if getErr != nil {
					err = getErr
				} else if revision.State == core.SkillRevisionDisabled {
					result = revision
				} else {
					result, err = assets.Store.TransitionSkillRevisionState(r.Context(), ws, input.RevisionID, input.ExpectedState, core.SkillRevisionDisabled)
				}
			}
		default:
			err = errors.New("unsupported skill lifecycle operation")
		}
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, 200, map[string]any{"operation": request.Operation, "result": result})
	}
}

func authorizeSkillMutation(ctx context.Context, svc *Service, actor, workspaceID, operation, target string) error {
	if actor == "" {
		return errors.New("actor is required")
	}
	if svc.SkillMutationAuthorizer == nil {
		return errors.New("skill mutation authorizer is required")
	}
	return svc.SkillMutationAuthorizer.AuthorizeSkillMutation(ctx, actor, workspaceID, operation, target)
}
func skillMutationTarget(raw json.RawMessage) string {
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	for _, key := range []string{"revision_id", "candidate_id", "skill_id", "resolution_id"} {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return "workspace"
}
func skillProjectRoot(svc *Service, workspaceID string) (string, error) {
	if strings.TrimSpace(svc.ProjectRoot) != "" && workspaceID == svc.Workspace {
		return svc.ProjectRoot, nil
	}
	manager, err := workspace.NewManager(svc.BaseDir)
	if err != nil {
		return "", err
	}
	project, err := manager.Project(workspaceID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(project.WorkspaceRoot) == "" {
		return "", errors.New("registered project root is required")
	}
	return project.WorkspaceRoot, nil
}
func parseSkillLimit(raw string) int {
	value := 20
	_, _ = fmt.Sscanf(raw, "%d", &value)
	if value < 1 {
		value = 20
	}
	if value > 200 {
		value = 200
	}
	return value
}
