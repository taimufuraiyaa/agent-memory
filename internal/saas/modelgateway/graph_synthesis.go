package modelgateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

type GraphSynthesisCommunity struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Summary            string   `json:"summary"`
	Findings           []string `json:"findings,omitempty"`
	CoveredSources     int      `json:"covered_sources"`
	UnresolvedEvidence int      `json:"unresolved_evidence"`
}

type GraphSynthesisInput struct {
	TenantID       string
	Provider       string
	Model          string
	Query          string
	Communities    []GraphSynthesisCommunity
	Evidence       []Evidence
	MaxPromptBytes int
}

// BuildGraphSynthesisRequest reduces Agent Memory-owned community context into
// the existing model gateway contract. Community reports are navigation hints;
// only separately supplied canonical Evidence can ground generated claims.
func BuildGraphSynthesisRequest(input GraphSynthesisInput) (GenerateRequest, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.Model) == "" || strings.TrimSpace(input.Query) == "" {
		return GenerateRequest{}, fmt.Errorf("graph synthesis identity and query are required")
	}
	if len(input.Evidence) == 0 || len(input.Evidence) > 128 || len(input.Communities) > 64 {
		return GenerateRequest{}, fmt.Errorf("graph synthesis requires bounded canonical evidence")
	}
	if input.MaxPromptBytes < 256 || input.MaxPromptBytes > 1<<20 {
		return GenerateRequest{}, fmt.Errorf("graph synthesis prompt bound is invalid")
	}
	for _, item := range input.Evidence {
		if strings.TrimSpace(item.SourceID) == "" || strings.TrimSpace(item.PassageID) == "" || strings.TrimSpace(item.Text) == "" {
			return GenerateRequest{}, fmt.Errorf("graph synthesis evidence is incomplete")
		}
	}
	communities, err := json.Marshal(input.Communities)
	if err != nil {
		return GenerateRequest{}, err
	}
	evidence, err := json.Marshal(input.Evidence)
	if err != nil {
		return GenerateRequest{}, err
	}
	prompt := "Answer the query using canonical evidence for every claim. Community reports are navigation context only and must not be cited as evidence. State coverage and unresolved evidence.\nQuery: " + strings.TrimSpace(input.Query) + "\nCommunity navigation context: " + string(communities) + "\nCanonical evidence: " + string(evidence)
	if len([]byte(prompt)) > input.MaxPromptBytes {
		return GenerateRequest{}, fmt.Errorf("graph synthesis prompt exceeds policy")
	}
	return GenerateRequest{TenantID: input.TenantID, Provider: input.Provider, Model: input.Model, Prompt: prompt, Evidence: append([]Evidence(nil), input.Evidence...)}, nil
}
