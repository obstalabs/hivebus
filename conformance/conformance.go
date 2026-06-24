// Package conformance is the shared, cross-repo wire-contract suite for the
// Hivebus /v0/agents protocol. It exists so that Hivebus and any external
// consumer (for example NeuroRouter Pro) cannot silently DRIFT: both vendor or
// import this package and run Compare against their own marshaled payloads in
// their own CI. If either side changes a wire field without an intentional,
// declared version bump, the golden fixtures fail the build on the side that
// diverged.
//
// Why this package is PUBLIC (not internal/, not a testdata directory): Go's
// internal/ rule makes internal packages unimportable across modules, and a
// testdata directory is not importable at all. A drift suite that only one repo
// can run defeats its own purpose. So the fixtures live as //go:embed'd JSON in a
// public package with a deliberately minimal exported surface: the wire structs,
// Version, the route names, and one Compare helper.
//
// FIELD-CHANGE DISCIPLINE (the version-lag rule lives here, with the code it
// governs, so it travels when a consumer vendors this package):
//   - Adding an OPTIONAL field (json:",omitempty") is a PATCH: existing golden
//     fixtures still match; regenerate to add coverage.
//   - Renaming, removing, retyping, or making-required any wire field is a
//     BREAKING change and MUST bump the MINOR version (pre-1.0) — and during the
//     change window both versions must be representable.
//   - A consumer MUST refuse to interoperate when its vendored conformance
//     Version differs from the peer's by more than ONE MINOR. That is the maximum
//     allowed version lag between Hivebus and an external consumer.
//
// Regenerate the golden fixtures after an intentional wire change with:
//
//	UPDATE_GOLDEN=1 go test ./conformance/...
package conformance

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
)

// Version is the wire-contract version these fixtures encode. It tracks the
// protocol contract version in internal/spec (spec.V0().Version). A consumer
// asserts its vendored Version is within one minor of the peer's; see the
// field-change discipline in the package doc.
const Version = "0.2.0"

// Route identifies a /v0/agents wire surface covered by the fixtures.
type Route string

const (
	RouteSessionRegister  Route = "agents.sessions.register"
	RouteSessionHeartbeat Route = "agents.sessions.heartbeat"
	RouteMessageSend      Route = "agents.messages.send"
	RouteMessageDeliver   Route = "agents.messages.deliver"
	RouteInbox            Route = "agents.sessions.inbox"
	RouteRoster           Route = "agents.sessions.roster"
)

// Routes returns every covered route in a stable order.
func Routes() []Route {
	rs := make([]Route, 0, len(goldenIndex))
	for r := range goldenIndex {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	return rs
}

//go:embed testdata/*.json
var goldenFS embed.FS

// goldenIndex maps each route to its golden fixture file. Adding a route means
// adding its file here and a sample in samples.go.
var goldenIndex = map[Route]string{
	RouteSessionRegister:  "testdata/agents_sessions_register.json",
	RouteSessionHeartbeat: "testdata/agents_sessions_heartbeat.json",
	RouteMessageSend:      "testdata/agents_messages_send.json",
	RouteMessageDeliver:   "testdata/agents_messages_deliver.json",
	RouteInbox:            "testdata/agents_sessions_inbox.json",
	RouteRoster:           "testdata/agents_sessions_roster.json",
}

// Golden returns the canonical wire bytes for a route (indented JSON).
func Golden(route Route) ([]byte, error) {
	path, ok := goldenIndex[route]
	if !ok {
		return nil, fmt.Errorf("conformance: unknown route %q", route)
	}
	data, err := goldenFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("conformance: read golden for %q: %w", route, err)
	}
	return data, nil
}

// Compare checks that a value, when JSON-marshaled, matches the golden fixture
// for route — field-for-field, order-insensitively (both sides are normalized
// through a generic decode). It returns a descriptive error on drift, naming the
// route, so a CI failure points straight at the diverged surface.
//
// A consumer calls Compare(route, itsMarshaledRequest) in its own CI to
// prove its wire types still match the shared contract.
func Compare(route Route, value any) error {
	got, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("conformance: marshal value for %q: %w", route, err)
	}
	want, err := Golden(route)
	if err != nil {
		return err
	}
	return compareJSON(route, got, want)
}

// CompareBytes is Compare for callers that already have marshaled bytes.
func CompareBytes(route Route, got []byte) error {
	want, err := Golden(route)
	if err != nil {
		return err
	}
	return compareJSON(route, got, want)
}

func compareJSON(route Route, got, want []byte) error {
	gotNorm, err := normalize(got)
	if err != nil {
		return fmt.Errorf("conformance: %q produced invalid JSON: %w", route, err)
	}
	wantNorm, err := normalize(want)
	if err != nil {
		return fmt.Errorf("conformance: golden for %q is invalid JSON: %w", route, err)
	}
	if !bytes.Equal(gotNorm, wantNorm) {
		return fmt.Errorf(
			"conformance: wire drift on %q\n  golden: %s\n  actual: %s\n(if this change is intentional, bump conformance.Version and regenerate with UPDATE_GOLDEN=1)",
			route, wantNorm, gotNorm,
		)
	}
	return nil
}

// normalize round-trips JSON through a generic value with sorted keys so that
// field ordering and insignificant whitespace never cause false drift.
func normalize(data []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v) // Go marshals map keys sorted, giving a stable form.
}
