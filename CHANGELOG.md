# Changelog

## [Unreleased]

### Added

- Public embedding and conformance surfaces for the agent-message runtime: `embed` serves Hivebus over a caller-provided listener, and `conformance` publishes importable golden fixtures for the `/v0/agents/*` wire contract.
- Standalone boardroom CLI commands: `hivebus say`, `hivebus inbox`, and `hivebus listen` let a non-orchestrated process send, read, and optionally acknowledge local Hivebus messages.
- Focused guide for standalone boardroom use and the embedding/conformance boundary.

### Fixed

- Conformance raw JSON comparisons now reject trailing data after the single JSON document, and inbox fixtures are verified against the real runtime HTTP response.

## [0.3.2] - 2026-06-17

### Added

- Prebuilt binaries for macOS and Linux (amd64/arm64) published on each tagged release, plus a Homebrew formula in `obstalabs/homebrew-tap`. Install without a Go toolchain.

## [0.3.1] - 2026-06-17

### Fixed

- `go install github.com/obstalabs/hivebus/cmd/hivebus@latest` now resolves to a valid release. The `v0.1.1` and `v0.2.0` tags predate the module-path migration and declare the old path; they are now `retract`ed in `go.mod` so the toolchain skips them.
- `hivebus version` now reports the real module version for `go install`-built binaries (via embedded build info) instead of the `dev` placeholder; `make build` still injects the exact version/commit/date.

## [0.3.0] - 2026-06-17

### Added

- `hivebus ask` read-only query/answer primitive for signed direct agent questions.
- `hivebus ask --server` live delivery for signed localhost ask/answer dogfood loops.
- `hivebus ask --insecure` for tokenless live ask against a `serve --auth-disabled` server.
- Local ask/answer dogfood conveniences: insecure ask self-registration, answer public-key files, and auth-disabled serve env skipping.
- `hivebus answer` conservative repo_status answerer loop for local signed ask/answer round trips.
- Answerer key discovery with ask-side `known_answerers` pinning for live ask verification.
- Signed repo_status observation context with repo ID, absolute git dir, and git-dir inode binding.
- Named observation binding levels (`git_dir_inode` / `git_dir_path` / `repo_id_only`) reported on every verify — no silent downgrade — plus `--expect-remote` and local-clone-derived remote-fingerprint binding for cross-machine asks.
- `docs/specs/observation-context-v0.md`: the dated, code-true specification of the observation-context binding and verification algorithm.
- `docs/guides/remote-ask-over-ssh.md`: runbook for running the ask/answer loop across two machines over an SSH reverse tunnel, with a verified transcript.
- README documents the cross-machine dispatch → remote-agent report-back flow, verified at the remote binding tier.
- `SECURITY.md`: disclosure policy and the honest envelope-signature verification state.
- Orthogonal envelope routing fields for visibility, scope, reply policy, redirects, and collection.
- Signed envelope schema primitives for query, answer, request, and authority directive messages.
- Typed NeuroRouter agent-run lifecycle envelopes (`nr.run.*`) — hivebus as receipts substrate for governed runs.

### Changed

- Quick Start rewritten as the "ride a second agent" loop: `go install`, then serve/answer/ask across terminals — no environment-variable ceremony.
- README explains the wider product stack (`workledger` is the CLI for [Hiveram](https://hiveram.com); NeuroRouter is the live-session bridge) so the open-core-vs-paid line is explicit.
- `hivebus ask --server` now exits 0 for delivered queries with no trusted answer and reports `delivered=true`, `answers=0`; delivery failures still exit non-zero.
- `hivebus ask --server` now prefers a later valid signed answer over an earlier unverified candidate in the same inbox batch.
- `hivebus answer --print-public-key` now requires `--signing-key` instead of printing a throwaway random key.
- Module path migrated to `github.com/obstalabs/hivebus`; LICENSE copyright is `Obsta Labs LLC`.
- Contributions now require a Developer Certificate of Origin sign-off (`git commit -s`); CI rejects unsigned-off pull request commits.

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
