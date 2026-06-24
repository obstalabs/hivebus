package conformance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/obstalabs/hivebus/conformance"
)

// TestWireConformance is the drift gate. For every covered route it marshals the
// canonical sample and compares it to the embedded golden fixture. On an
// intentional wire change, regenerate with UPDATE_GOLDEN=1 (which rewrites the
// testdata files); CI runs without that env, so any unintended drift fails here.
//
// An external consumer writes the mirror of this test against ITS own wire
// types using conformance.Compare — same fixtures, so a divergence on either side
// fails that side's build.
func TestWireConformance(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, route := range conformance.Routes() {
		route := route
		t.Run(string(route), func(t *testing.T) {
			sample := conformance.Sample(route)
			if sample == nil {
				t.Fatalf("no sample defined for route %q", route)
			}

			if update {
				writeGolden(t, route, sample)
				return
			}

			if err := conformance.Compare(route, sample); err != nil {
				t.Fatalf("%v", err)
			}
		})
	}
}

// TestEveryRouteHasGoldenAndSample guards completeness: the route table, the
// sample table, and the embedded fixtures must all agree, so a new route can't be
// half-added (sample but no fixture, or vice versa).
func TestEveryRouteHasGoldenAndSample(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		t.Skip("regeneration run")
	}
	for _, route := range conformance.Routes() {
		if conformance.Sample(route) == nil {
			t.Errorf("route %q has no Sample()", route)
		}
		if _, err := conformance.Golden(route); err != nil {
			t.Errorf("route %q has no golden fixture: %v", route, err)
		}
	}
}

// writeGolden writes the indented canonical JSON for a route under testdata/.
func writeGolden(t *testing.T, route conformance.Route, sample any) {
	t.Helper()
	data, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden for %q: %v", route, err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", goldenFileName(route))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// goldenFileName mirrors the mapping in conformance.go's goldenIndex.
func goldenFileName(route conformance.Route) string {
	switch route {
	case conformance.RouteSessionRegister:
		return "agents_sessions_register.json"
	case conformance.RouteSessionHeartbeat:
		return "agents_sessions_heartbeat.json"
	case conformance.RouteMessageSend:
		return "agents_messages_send.json"
	case conformance.RouteMessageDeliver:
		return "agents_messages_deliver.json"
	case conformance.RouteInbox:
		return "agents_sessions_inbox.json"
	case conformance.RouteRoster:
		return "agents_sessions_roster.json"
	default:
		return string(route) + ".json"
	}
}
