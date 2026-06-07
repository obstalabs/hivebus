# Changelog

## [Unreleased]

### Added

- `hivebus ask` read-only query/answer primitive for signed direct agent questions.
- `hivebus ask --server` live delivery for signed localhost ask/answer dogfood loops.
- `hivebus ask --insecure` for tokenless live ask against a `serve --auth-disabled` server.
- Local ask/answer dogfood conveniences: insecure ask self-registration, answer public-key files, and auth-disabled serve env skipping.
- `hivebus answer` conservative repo_status answerer loop for local signed ask/answer round trips.
- Signed repo_status observation context with repo ID, absolute git dir, and git-dir inode binding.
- Orthogonal envelope routing fields for visibility, scope, reply policy, redirects, and collection.
- Signed envelope schema primitives for query, answer, request, and authority directive messages.
- Typed NeuroRouter agent-run lifecycle envelopes (`nr.run.*`) — hivebus as receipts substrate for governed runs.

### Changed

- `hivebus ask --server` now exits 0 for delivered queries with no trusted answer and reports `delivered=true`, `answers=0`; delivery failures still exit non-zero.
- `hivebus ask --server` now prefers a later valid signed answer over an earlier unverified candidate in the same inbox batch.
- `hivebus answer --print-public-key` now requires `--signing-key` instead of printing a throwaway random key.

## [0.2.0] - 2026-04-20

- Add worker leases, runtime auth, dispatch, context snapshots, and resume capsules for the v0 HTTP runtime.
- Add artifact manifests, thread artifact storage, SSE thread watching, partial result streaming, and clarification request flow.
- Define capability lifecycle, artifact trust, edge routing, authorization, clarification lifecycle, participant type, and channel access schemas.
- Add nullbot intake, work-order bridge promotion, agent messaging queues, channel access control, and inferred worker capabilities.
- Add unified Obstalabs `ol_` license verification for billing v2 entitlements and document the shared billing contract.
- Clarify that deployment location is not the edition boundary: Free stays self-hostable, Pro is single-tenant on any deployment target, Teams adds shared coordination, and Enterprise adds corporate controls.

## [0.1.1] - 2026-04-15

- Add `hivebus serve` as the first runnable v0 HTTP runtime.
- Persist thread events in an append-only SQLite log with deterministic replay.
- Expose thread create, append, replay, and health endpoints for local smoke use.
- Document the new runtime surface and update wording around optional Hiveram execution integration.

## [0.1.0] - 2026-03-31

- Scaffold Hivebus as a Go project with CLI, CI, release config, and documentation.
- Add typed protocol models for envelopes, threads, diagnoses, and artifacts.
- Add edition policy limits and deterministic work-order drafting rules.
- Add a sample nullbot-to-workledger case bundle for the v0 protocol.
- Clarify the repo split: free stays in `hivebus`, while non-free tiers live in `hivebus-pro`.
- Define the community vs paid feature boundary and formalize the canonical workledger integration contract.
- Name the paid-side background guidance layer `Adaptive Optimization`.
