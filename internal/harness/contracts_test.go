package harness

import (
	"context"
	"errors"
	"math"
	"testing"
)

type fakeDecision struct {
	manifest Manifest
	state    AccessState
	answer   Outcome
}

func (f *fakeDecision) Probe(_ context.Context, scope Scope) (LiveAccess, error) {
	return LiveAccess{Version: ContractVersion, Provider: f.manifest.ID, Scope: scope, Revision: 1,
		Capabilities: map[CapabilityID]AccessState{f.manifest.Capabilities[0]: f.state}}, nil
}
func (f *fakeDecision) Close() error { return nil }
func (f *fakeDecision) Decide(_ context.Context, q DecisionQuestion) (DecisionAnswer, error) {
	return DecisionAnswer{Envelope: q.Envelope, Outcome: f.answer, Selected: []string{"candidate"}, Confidence: 0.7}, nil
}

type fakeModel struct{ fakeDecision }

func (f *fakeModel) Generate(_ context.Context, q ModelRequest) (ModelAnswer, error) {
	return ModelAnswer{Envelope: q.Envelope, Outcome: OutcomeOK, Text: "bounded"}, nil
}

type fakeTool struct{ fakeDecision }

func (f *fakeTool) Prepare(_ context.Context, q ToolRequest) (PreparedAction, error) {
	return PreparedAction{Envelope: q.Envelope, Outcome: OutcomeOK, Digest: "fake-digest", Summary: "fake read"}, nil
}
func (f *fakeTool) Invoke(_ context.Context, action PreparedAction) (ToolAnswer, error) {
	return ToolAnswer{Envelope: action.Envelope, Outcome: OutcomeOK, Output: []byte("fake result")}, nil
}

func TestRegistryAndProviderContracts(t *testing.T) {
	registry := NewRegistry()
	first := Manifest{Version: ContractVersion, ID: "fake-full", Kind: KindDecision, Capabilities: []CapabilityID{"visibility"}}
	second := Manifest{Version: ContractVersion, ID: "fake-partial", Kind: KindDecision, Capabilities: []CapabilityID{"tool-rank"}}
	for _, tc := range []struct {
		manifest Manifest
		state    AccessState
		outcome  Outcome
	}{
		{first, AccessAvailable, OutcomeOK},
		{second, AccessAvailable, OutcomePartial},
	} {
		tc := tc
		if err := registry.Register(tc.manifest, func() (Provider, error) {
			return &fakeDecision{manifest: tc.manifest, state: tc.state, answer: tc.outcome}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(first, func() (Provider, error) { return nil, nil }); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	if got := registry.Manifests(); len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("manifests = %+v", got)
	}
	for _, manifest := range registry.Manifests() {
		opened, provider, err := registry.Open(manifest.ID)
		if err != nil || opened.ID != manifest.ID {
			t.Fatalf("open %s = %v", manifest.ID, err)
		}
		scope := Scope{Workspace: "agent-memory", Generation: 4}
		access, err := provider.Probe(context.Background(), scope)
		if err != nil || access.Validate(manifest, scope) != nil {
			t.Fatalf("probe %s = %v, %+v", manifest.ID, err, access)
		}
		envelope := Envelope{Version: ContractVersion, Provider: manifest.ID, Scope: scope,
			AccessRevision: access.Revision, Capability: manifest.Capabilities[0], MaxBytes: 128}
		if err := envelope.Validate(access); err != nil {
			t.Fatalf("envelope %s = %v", manifest.ID, err)
		}
		question := DecisionQuestion{Envelope: envelope, Kind: string(manifest.Capabilities[0]), Candidates: []string{"candidate"}}
		answer, err := provider.(DecisionProvider).Decide(context.Background(), question)
		if err != nil || ValidateDecisionAnswer(question, answer) != nil {
			t.Fatalf("answer %s = %v, %+v", manifest.ID, err, answer)
		}
		_ = provider.Close()
	}
}

func TestModelAndToolPortsUseSameAccessEnvelope(t *testing.T) {
	registry := NewRegistry()
	model := Manifest{Version: ContractVersion, ID: "fake-model", Kind: KindModel, Capabilities: []CapabilityID{"generation"}}
	tool := Manifest{Version: ContractVersion, ID: "fake-tool", Kind: KindTool, Capabilities: []CapabilityID{"read"}}
	if err := registry.Register(model, func() (Provider, error) {
		return &fakeModel{fakeDecision{manifest: model, state: AccessAvailable}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool, func() (Provider, error) {
		return &fakeTool{fakeDecision{manifest: tool, state: AccessAvailable}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range []Manifest{model, tool} {
		_, provider, err := registry.Open(manifest.ID)
		if err != nil {
			t.Fatal(err)
		}
		scope := Scope{Workspace: "agent-memory", Generation: 1}
		access, err := provider.Probe(context.Background(), scope)
		if err != nil || access.Validate(manifest, scope) != nil {
			t.Fatalf("access = %+v, %v", access, err)
		}
		envelope := Envelope{Version: ContractVersion, Provider: manifest.ID, Scope: scope,
			AccessRevision: access.Revision, Capability: manifest.Capabilities[0], MaxBytes: 64}
		if err := envelope.Validate(access); err != nil {
			t.Fatal(err)
		}
		switch typed := provider.(type) {
		case ModelProvider:
			answer, err := typed.Generate(context.Background(), ModelRequest{Envelope: envelope, MaxOutputTokens: 8})
			if err != nil || ValidateReply(envelope, answer.Envelope, answer.Outcome, len(answer.Text)) != nil {
				t.Fatalf("model answer = %+v, %v", answer, err)
			}
		case ToolProvider:
			prepared, err := typed.Prepare(context.Background(), ToolRequest{Envelope: envelope, ToolID: "read"})
			if err != nil || ValidateReply(envelope, prepared.Envelope, prepared.Outcome, len(prepared.Summary)) != nil {
				t.Fatalf("prepared = %+v, %v", prepared, err)
			}
			answer, err := typed.Invoke(context.Background(), prepared)
			if err != nil || ValidateReply(envelope, answer.Envelope, answer.Outcome, len(answer.Output)) != nil {
				t.Fatalf("tool answer = %+v, %v", answer, err)
			}
		default:
			t.Fatal("unexpected provider port")
		}
		_ = provider.Close()
	}
}

func TestContractRejectsInvalidAndStaleData(t *testing.T) {
	manifest := Manifest{Version: ContractVersion, ID: "fake", Kind: KindDecision, Capabilities: []CapabilityID{"visibility"}}
	for _, invalid := range []Manifest{
		{Version: 2, ID: "fake", Kind: KindDecision, Capabilities: []CapabilityID{"visibility"}},
		{Version: 1, ID: "fake", Kind: KindDecision, Capabilities: []CapabilityID{"visibility", "visibility"}},
		{Version: 1, ID: "fake", Kind: KindDecision},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("accepted invalid manifest %+v", invalid)
		}
	}
	scope := Scope{Workspace: "agent-memory", Generation: 2}
	access := LiveAccess{Version: ContractVersion, Provider: manifest.ID, Scope: scope, Revision: 3,
		Capabilities: map[CapabilityID]AccessState{"visibility": AccessAvailable}}
	if err := access.Validate(manifest, Scope{Workspace: "other", Generation: 2}); !errors.Is(err, ErrStale) {
		t.Fatalf("cross-scope access = %v", err)
	}
	envelope := Envelope{Version: ContractVersion, Provider: manifest.ID, Scope: scope,
		AccessRevision: access.Revision, Capability: "visibility", MaxBytes: 16}
	if err := envelope.Validate(access); err != nil {
		t.Fatal(err)
	}
	stale := envelope
	stale.Scope.Generation++
	if err := ValidateReply(envelope, stale, OutcomeOK, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("late reply = %v", err)
	}
	if err := ValidateReply(envelope, envelope, OutcomeOK, 17); err == nil {
		t.Fatal("accepted oversized reply")
	}
	access.Capabilities["visibility"] = AccessDenied
	if err := envelope.Validate(access); err == nil {
		t.Fatal("accepted denied capability")
	}
	access.Capabilities["visibility"] = AccessAvailable
	access.Capabilities["undeclared"] = AccessAvailable
	if err := access.Validate(manifest, scope); err == nil {
		t.Fatal("accepted undeclared capability")
	}
	question := DecisionQuestion{Envelope: envelope, Candidates: []string{"safe"}}
	for _, answer := range []DecisionAnswer{
		{Envelope: envelope, Outcome: OutcomeOK, Selected: []string{"unsafe"}, Confidence: 0.5},
		{Envelope: envelope, Outcome: OutcomeOK, Selected: []string{"safe"}, Confidence: math.NaN()},
		{Envelope: envelope, Outcome: OutcomeOK, Selected: []string{"safe", "safe"}, Confidence: 0.5},
	} {
		if err := ValidateDecisionAnswer(question, answer); err == nil {
			t.Fatalf("accepted invalid decision %+v", answer)
		}
	}
}

func TestRegistryRejectsWrongKindFactory(t *testing.T) {
	r := NewRegistry()
	m := Manifest{Version: ContractVersion, ID: "wrong-kind", Kind: KindModel, Capabilities: []CapabilityID{"generation"}}
	if err := r.Register(m, func() (Provider, error) { return &fakeDecision{manifest: m}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Open(m.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong kind = %v", err)
	}
	if _, _, err := r.Open("missing"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("missing = %v", err)
	}
}
