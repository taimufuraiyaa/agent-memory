package retrieval

import (
	"errors"
	"strings"
	"testing"
)

func TestGraphIntentKeepsDirectQuestionsBasicAndRoutesRelationalAndGlobal(t *testing.T) {
	router := NewGraphRouter()
	policy := GraphRoutePolicy{GraphEnabled: true, AllowLocal: true, AllowGlobal: true}
	available := GraphRouteAvailability{Readable: true, Fresh: true, ActiveRevisionID: "revision-a"}
	cases := []struct {
		query  string
		intent GraphIntent
		mode   GraphQueryMode
	}{
		{"Who owns payment retry logic?", GraphIntentDirect, GraphQueryBasic},
		{"How is the retry handler related to checkout incidents?", GraphIntentRelational, GraphQueryLocal},
		{"What failure patterns appear most frequently across all incident reports?", GraphIntentGlobal, GraphQueryGlobal},
	}
	for _, test := range cases {
		decision, err := router.Route(GraphRouteRequest{Mode: GraphQueryAuto, Query: test.query, Policy: policy, Availability: available})
		if err != nil {
			t.Fatalf("query %q: %v", test.query, err)
		}
		if decision.Intent != test.intent || decision.SelectedMode != test.mode || decision.Fallback {
			t.Fatalf("query %q decision=%+v", test.query, decision)
		}
	}
}

func TestGraphRouteBasicIsTheSafeDefaultAndCallerBasicCannotBeUpgraded(t *testing.T) {
	router := NewGraphRouter()
	for _, mode := range []GraphQueryMode{"", GraphQueryBasic} {
		decision, err := router.Route(GraphRouteRequest{
			Mode: mode, Query: "patterns across all documents",
			Policy:       GraphRoutePolicy{GraphEnabled: true, AllowLocal: true, AllowGlobal: true},
			Availability: GraphRouteAvailability{Readable: true, Fresh: true, ActiveRevisionID: "revision-a"},
		})
		if err != nil || decision.SelectedMode != GraphQueryBasic || decision.Fallback {
			t.Fatalf("basic-safe mode %q decision=%+v err=%v", mode, decision, err)
		}
	}
}

func TestGraphFallbackIsExplicitForPolicyUnavailableAndStaleIndex(t *testing.T) {
	router := NewGraphRouter()
	cases := []struct {
		request GraphRouteRequest
		reason  GraphRouteReason
	}{
		{GraphRouteRequest{Mode: GraphQueryLocal, Query: "connect a and b"}, GraphReasonPolicyDisabled},
		{GraphRouteRequest{Mode: GraphQueryLocal, Query: "connect a and b", Policy: GraphRoutePolicy{GraphEnabled: true, AllowLocal: true}}, GraphReasonIndexUnavailable},
		{GraphRouteRequest{Mode: GraphQueryGlobal, Query: "across all", Policy: GraphRoutePolicy{GraphEnabled: true, AllowGlobal: true}, Availability: GraphRouteAvailability{Readable: true, Fresh: false, ActiveRevisionID: "revision-old"}}, GraphReasonIndexStale},
	}
	for _, test := range cases {
		decision, err := router.Route(test.request)
		if err != nil {
			t.Fatal(err)
		}
		if decision.SelectedMode != GraphQueryBasic || !decision.Fallback || !decision.Degraded || decision.ReasonCode != test.reason {
			t.Fatalf("unexpected fallback: %+v", decision)
		}
	}
}

func TestGraphRouteRequiredReturnsBoundedTypedError(t *testing.T) {
	query := "private tenant text that must not enter an error"
	_, err := NewGraphRouter().Route(GraphRouteRequest{
		Mode: GraphQueryGlobal, Query: query, RequireGraph: true,
		Policy:       GraphRoutePolicy{GraphEnabled: true, AllowGlobal: true},
		Availability: GraphRouteAvailability{Readable: true, Fresh: false, ActiveRevisionID: "private-revision-id"},
	})
	if !errors.Is(err, ErrGraphRouteRequired) {
		t.Fatalf("expected typed required-route error, got %v", err)
	}
	if strings.Contains(err.Error(), query) || strings.Contains(err.Error(), "private-revision-id") || len(err.Error()) > 160 {
		t.Fatalf("route error was unbounded or disclosed request data: %q", err)
	}
}

func TestGraphRouteMayUsePolicyApprovedStaleRevisionWithWarning(t *testing.T) {
	decision, err := NewGraphRouter().Route(GraphRouteRequest{
		Mode: GraphQueryLocal, Query: "relationship", Policy: GraphRoutePolicy{GraphEnabled: true, AllowLocal: true, AllowStale: true},
		Availability: GraphRouteAvailability{Readable: true, Fresh: false, ActiveRevisionID: "revision-old"},
	})
	if err != nil || decision.SelectedMode != GraphQueryLocal || decision.Fallback || !decision.Degraded || decision.ReasonCode != GraphReasonIndexStaleAllowed {
		t.Fatalf("stale policy decision=%+v err=%v", decision, err)
	}
}
