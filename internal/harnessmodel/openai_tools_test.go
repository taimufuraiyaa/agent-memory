package harnessmodel

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
)

func toolBody(name, args string) string {
	return `{"status":"completed","model":"gpt-x","output":[{"type":"function_call","name":"` + name + `","arguments":` + args + `}],"usage":{"input_tokens":50,"output_tokens":5}}`
}

func describer(names ...string) func([]string) []openaiapi.Tool {
	return func(ids []string) []openaiapi.Tool {
		var out []openaiapi.Tool
		for _, id := range ids {
			for _, n := range names {
				if id == n {
					out = append(out, openaiapi.Tool{Name: id, Description: "d " + id, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
				}
			}
		}
		return out
	}
}

func toolSession(t *testing.T, f *fakeOpenAI, tools func([]string) []openaiapi.Tool) *harness.Session {
	t.Helper()
	p, err := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return testKey }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true, Timeout: 5 * time.Second, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	return sessionFor(t, p)
}

func ask(t *testing.T, s *harness.Session, offered ...string) harness.ModelAnswer {
	t.Helper()
	envelope, _ := s.Envelope(OpenAICapability, 4096)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "do it", ToolSchemaIDs: offered})
	if err != nil {
		t.Fatalf("a provider problem must be a typed outcome: %v", err)
	}
	return answer
}

func TestOfferedToolsGoToTheServiceAsFunctionsAndAnUnofferedOneNeverDoes(t *testing.T) {
	f := newFakeOpenAI(t)
	s := toolSession(t, f, describer("read_file", "search"))
	f.set(200, toolBody("read_file", `"{\"path\":\"a.go\"}"`))
	answer := ask(t, s, "read_file", "search", "unknown_tool")
	if answer.Outcome != harness.OutcomeOK || answer.ToolID != "read_file" || string(answer.ToolArguments) != `{"path":"a.go"}` {
		t.Fatalf("%+v", answer)
	}
	var sent struct {
		Tools []struct {
			Type, Name string
			Strict     bool
		}
		ToolChoice string `json:"tool_choice"`
		Parallel   *bool  `json:"parallel_tool_calls"`
		Store      bool
	}
	if err := json.Unmarshal(f.last().body, &sent); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range sent.Tools {
		names = append(names, tool.Name)
		if tool.Type != "function" {
			t.Errorf("tool type %q", tool.Type)
		}
	}
	if !reflect.DeepEqual(names, []string{"read_file", "search"}) || sent.ToolChoice != "auto" || sent.Parallel == nil || *sent.Parallel || sent.Store {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestACallToAToolThatWasNotOfferedIsAFailureAndNeverPassedOn(t *testing.T) {
	f := newFakeOpenAI(t)
	s := toolSession(t, f, describer("read_file"))
	f.set(200, toolBody("delete_file", `"{}"`))
	if answer := ask(t, s, "read_file"); answer.Outcome == harness.OutcomeOK || answer.ToolID != "" || len(answer.ToolArguments) != 0 {
		t.Fatalf("%+v", answer)
	}
}

func TestWithoutToolsOrOfferedIDsTheRequestIsTextOnly(t *testing.T) {
	for name, tools := range map[string]func([]string) []openaiapi.Tool{"no describer": nil, "a describer that knows none": describer("other")} {
		f := newFakeOpenAI(t)
		s := toolSession(t, f, tools)
		ask(t, s, "read_file")
		if strings.Contains(string(f.last().body), `"tools"`) {
			t.Errorf("%s: tools were sent: %s", name, f.last().body)
		}
	}
	f := newFakeOpenAI(t)
	s := toolSession(t, f, describer("read_file"))
	ask(t, s) // nothing offered
	if strings.Contains(string(f.last().body), `"tools"`) {
		t.Error("tools were sent when none were offered")
	}
}

func TestAClarifyCallBecomesAQuestionNotATool(t *testing.T) {
	f := newFakeOpenAI(t)
	s := toolSession(t, f, describer("clarify"))
	f.set(200, toolBody("clarify", `"{\"question\":\"Which branch?\"}"`))
	answer := ask(t, s, "clarify")
	if answer.Outcome != harness.OutcomeOK || answer.ToolID != "clarify" || answer.Text != "Which branch?" || len(answer.ToolArguments) != 0 {
		t.Fatalf("%+v", answer)
	}
	for _, bad := range []string{`"{}"`, `"{\"question\":\"  \"}"`, `"[1]"`} {
		f.set(200, toolBody("clarify", bad))
		if answer := ask(t, s, "clarify"); answer.Outcome == harness.OutcomeOK {
			t.Errorf("clarify with %s was accepted: %+v", bad, answer)
		}
	}
}

func TestAMalformedOrOversizedFunctionCallIsRefused(t *testing.T) {
	f := newFakeOpenAI(t)
	s := toolSession(t, f, describer("read_file"))
	for name, body := range map[string]string{
		"a bad name":         toolBody("../etc", `"{}"`),
		"arguments not json": toolBody("read_file", `"{not json"`),
		"huge arguments":     toolBody("read_file", `"`+strings.Repeat("a", openaiapi.MaxToolArgumentBytes+10)+`"`),
	} {
		f.set(200, body)
		if answer := ask(t, s, "read_file"); answer.Outcome == harness.OutcomeOK {
			t.Errorf("%s was accepted: %+v", name, answer)
		}
	}
	// A reply too big for its envelope is a failure, not a contract error.
	f.set(200, toolBody("read_file", `"{\"path\":\"`+strings.Repeat("x", 300)+`\"}"`))
	envelope, _ := s.Envelope(OpenAICapability, 64)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "do it", ToolSchemaIDs: []string{"read_file"}})
	if err != nil || answer.Outcome == harness.OutcomeOK || answer.ToolID != "" {
		t.Fatalf("%+v %v", answer, err)
	}
	if !offered([]openaiapi.Tool{{Name: "a"}}, "a") || offered(nil, "a") || offered([]openaiapi.Tool{{Name: "b"}}, "a") {
		t.Fatal("offered is wrong")
	}
}
