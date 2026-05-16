[![CI](https://github.com/ppiankov/hivebus/actions/workflows/ci.yml/badge.svg)](https://github.com/ppiankov/hivebus/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8.svg)](https://go.dev)

# hivebus

Secure threaded coordination fabric for agent-native issue intake, investigation, and workledger-first work-order creation.

## What This Is

Hivebus is a machine-native bus for agents and services that need more than raw RPC or generic queues. It treats an issue as a typed thread with structured envelopes, evidence, provenance, state transitions, and a hard gate before a work order can be created.

The product shape behind this repo is:

- `nullbot` runs close to the problem, gathers safe evidence, and opens a thread.
- specialist agents investigate asynchronously inside the same thread.
- Hivebus preserves receipts, context, and auditability as JSON-first protocol objects.
- once the diagnosis is verified, Hivebus drafts a work order for `workledger`, with optional execution integration to `hiveram.com`.
- this repo owns the free/core surface; non-free editions live in the separate `hivebus-pro` repo.

## Licensing Model

This repository follows the same model as `neurorouter-free`: it is the maintenance-focused community/core edition of Hivebus.

- this repo keeps the indispensable protocol core public and self-hostable
- `hivebus-pro` owns paid-only capability
- new product capability does not land here by default unless a tracked work order explicitly expands the public boundary

Commercial editions use the shared Obstalabs billing contract:

- the billing service issues unified `ol_` license keys, not product-prefixed `hb_` keys
- commercial Hivebus surfaces verify licenses with `OL_LICENSE_VERIFY_KEY`
- the signed payload contains `products[]` plus `entitlements[]`; Hivebus requires an entitlement with `product=hivebus` and reads its tier from `tier`
- checkout starts at `/v1/billing/checkout`, post-checkout license retrieval uses `/v1/billing/license`, and account management uses the billing portal
- this open-source runtime keeps working without billing unless a commercial integration explicitly calls the license verifier

## Community Vs Paid

Hivebus only becomes essential if it carries the whole path from issue intake to tracked execution. That means the free/community line is not a crippled toy: it includes the protocol core and the canonical `workledger` bridge. Paid tiers add hosted, commercial, org, and compliance layers on top.

Deployment location is not the tier split. Free/core stays self-hostable, Pro can run single-tenant anywhere, Teams adds shared coordination, and Enterprise adds corporate controls. Local, Fly, VPS, and private infrastructure are all valid deployment targets.

| Capability | Free | Pro | Teams | Enterprise | Repo |
|------------|------|-----|-------|------------|------|
| Typed JSON envelopes, threads, receipts, artifacts, and lifecycle state | yes | yes | yes | yes | `hivebus` |
| NeuroRouter `nr.run.*` receipt envelope shapes for governed agent runs | yes | yes | yes | yes | `hivebus` |
| Self-hosted bus core and deterministic validation/routing primitives | yes | yes | yes | yes | `hivebus` |
| Nullbot intake core and clarification loop | yes | yes | yes | yes | `hivebus` |
| Canonical `workledger` bridge: search, create, update, note, claim, release, context sync | yes | yes | yes | yes | `hivebus` |
| Adaptive Optimization: background analysis, selective hints, escalation to Vectorcourt or human leads | no | yes | yes | yes | `hivebus-pro` |
| Optional `hiveram.com` execution integration | no | yes | yes | yes | `hivebus-pro` |
| Managed hosted bus/control plane | no | yes | yes | yes | `hivebus-pro` |
| Shared queues, RBAC, team/org policy packs | no | no | yes | yes | `hivebus-pro` |
| Enterprise retention, BYOK, regional controls, audit exports | no | no | no | yes | `hivebus-pro` |

This is the separation line: free owns the structured conversation substrate plus canonical execution tracking; paid owns the commercial and organizational layers that sit on top.

## What This Is NOT

- Not a human chat app with a GUI.
- Not a generic message queue replacement.
- Not an agent marketplace.
- Not autonomous remediation by default.
- Not a prompt soup relay where untyped blobs bounce between models.

## Philosophy

Hivebus follows RootOps principles:

- Prevent unsafe work structurally instead of apologizing afterward.
- Keep the protocol explicit, typed, and replayable.
- Separate intake, investigation, and execution instead of hiding them inside one opaque model call.
- Refuse to create a work order until the diagnosis is verified and missing information is resolved.
- Make pricing tiers policy boundaries, not protocol forks.

## Quick Start

```bash
make build
./bin/hivebus serve --db /tmp/hivebus.db
./bin/hivebus spec
./bin/hivebus sample-case
make test
```

## Usage

Print the protocol contract:

```bash
./bin/hivebus spec
```

Print a concrete nullbot-to-workledger example:

```bash
./bin/hivebus sample-case
```

The `nr.run.*` receipt protocol is documented in
[docs/protocols/nr-run-envelopes.md](docs/protocols/nr-run-envelopes.md).

Show build metadata:

```bash
./bin/hivebus version --json
```

Run the v0 HTTP runtime with a SQLite append-only event log:

```bash
./bin/hivebus serve --listen 127.0.0.1:7081 --db /tmp/hivebus.db
```

Run the runtime with self-validating Ed25519 API keys:

```bash
HIVEBUS_API_VERIFY_KEY=<base64-ed25519-public-key> \
  ./bin/hivebus serve --listen 127.0.0.1:7081 --db /tmp/hivebus.db
```

Release builds can embed the same public verify key at build time:

```bash
HIVEBUS_API_VERIFY_KEY=<base64-ed25519-public-key> make build
```

Fetch the compact recovery handoff for a promoted thread:

```bash
curl -H "Authorization: Bearer <operator-token>" \
  http://127.0.0.1:7081/v0/threads/<thread-id>/recovery
```

The recovery capsule contains verified diagnosis, evidence refs, stale/rejected fact labels, and the local promotion receipt. It deliberately excludes raw transcript, model narration, artifact bodies, and unlabeled stale hypotheses.

## Architecture

```text
nullbot collector
  -> creates a thread and evidence bundle
  -> sends signed task.request envelopes into Hivebus

Hivebus core
  -> validates envelope structure and tier policy
  -> preserves thread state in an append-only SQLite event log
  -> routes investigation work across specialist agents

investigator agents
  -> ask clarification.request when evidence is incomplete
  -> emit diagnosis.proposed only when evidence is sufficient

workledger bridge
  -> accepts only verified diagnoses
  -> creates the canonical work order that can fully resolve the user story
  -> leaves a compact recovery capsule for post-compaction handoff

optional hiveram.com execution integration
  -> mirrors or extends the same work order for commercial workflows when needed
```

Current code layout:

- `cmd/hivebus`: minimal CLI entrypoint
- `internal/model`: envelopes, threads, artifacts, diagnoses, recovery capsules
- `internal/runtime`: v0 HTTP handlers for intake, promotion, dispatch, thread creation, append, and replay
- `internal/policy`: free, pro, teams, enterprise limits
- `internal/spec`: exported v0 contract and sample case bundle
- `internal/store`: SQLite append-only event log, deterministic replay, and verified recovery projections
- `internal/work`: deterministic workledger drafting rules with optional Hiveram execution targets

## Editions

Hivebus keeps one protocol across all editions. The differences live in coordination and policy complexity, not deployment topology:

- `free`: self-hostable protocol core, smallest retention window, and artifact limits for solo experiments, implemented in this `hivebus` repo.
- `pro`: single-tenant production coordination with longer retention and commercial runtime capability, implemented in `hivebus-pro`, and deployable anywhere.
- `teams`: shared coordination, multi-operator policy, and larger routing/evidence limits, implemented in `hivebus-pro`.
- `enterprise`: corporate controls, compliance posture, and governance surfaces, implemented in `hivebus-pro`.

This keeps the protocol shared while making the repo boundary explicit: free stays open here, non-free stays out of the OSS tree, and no edition requires a specific hosting location.

## Canonical Workledger Contract

`workledger` is the execution source of truth for Hivebus. A verified diagnosis is not enough on its own; it must be promotable into a canonical work order with an explicit target project.

After promotion, `GET /v0/threads/{threadID}/recovery` exposes a verified-provenance capsule for compaction recovery and handoff. It is generated from local Hivebus events, so it does not require Hiveram, NeuroRouter, or a live Workledger call to read. External IDs are additive references, not hidden dependencies.

The free/community contract includes these workledger operations:

- search before create to avoid duplicate work orders
- create and update the canonical work order
- add notes with agent findings, evidence, and commit SHAs
- claim and release work so agents do not execute the same fix twice
- sync context blobs across machines and operator sessions
- update project metadata when the bridge needs repo or projection state

`hiveram.com` is optional and commercial. It may mirror or present the same work order, but it does not replace `workledger` as the ledger of record.

Adaptive Optimization is also paid-only. It is the background intelligence layer that analyzes privacy-safe pattern events, produces selective evidence-backed hints, and can escalate to Vectorcourt or human leads when the operator opts in.

## Known Limitations

- The v0 runtime is HTTP-only and intentionally small: nullbot intake, thread promotion, dispatch, append, and replay.
- Envelope signatures are represented structurally but not cryptographically verified yet.
- The workledger bridge requires explicit `WORKLEDGER_URL` or `WORKLEDGER_HOST` plus `WORKLEDGER_API_KEY` configuration on the runtime host.
- Optional `hiveram.com` sync is exposed as a hook surface, not a bundled free-runtime integration.
- Capability routing is still declarative rather than runtime-driven.
- Runtime auth validates Ed25519-signed API keys locally, with hashed token files retained only as an explicit local fallback.

## Roadmap

- Add signed envelope verification and nonce replay protection.
- Add additional nullbot intake adapters beyond the v0 HTTP path.
- Extend the workledger bridge with richer search/update/note flows and keep optional Hiveram execution integration in `hivebus-pro`.
- Keep non-free runtime surfaces in `hivebus-pro` instead of mixing them into this repo.
- Add queue-backed and realtime transports without changing protocol shape.

## License

MIT. See [LICENSE](LICENSE).
