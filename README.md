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
- once the diagnosis is verified, Hivebus drafts a work order for `workledger`, with optional sync to `hiveram.com`.
- this repo owns the free/core surface; non-free editions live in the separate `hivebus-pro` repo.

## Licensing Model

This repository follows the same model as `neurorouter-free`: it is the maintenance-focused community/core edition of Hivebus.

- this repo keeps the indispensable protocol core public and self-hostable
- `hivebus-pro` owns paid-only capability
- new product capability does not land here by default unless a tracked work order explicitly expands the public boundary

## Community Vs Paid

Hivebus only becomes essential if it carries the whole path from issue intake to tracked execution. That means the free/community line is not a crippled toy: it includes the protocol core and the canonical `workledger` bridge. Paid tiers add hosted, commercial, org, and compliance layers on top.

| Capability | Free | Pro | Teams | Enterprise | Repo |
|------------|------|-----|-------|------------|------|
| Typed JSON envelopes, threads, receipts, artifacts, and lifecycle state | yes | yes | yes | yes | `hivebus` |
| Self-hosted bus core and deterministic validation/routing primitives | yes | yes | yes | yes | `hivebus` |
| Nullbot intake core and clarification loop | yes | yes | yes | yes | `hivebus` |
| Canonical `workledger` bridge: search, create, update, note, claim, release, context sync | yes | yes | yes | yes | `hivebus` |
| Adaptive Optimization: background analysis, selective hints, escalation to Vectorcourt or human leads | no | yes | yes | yes | `hivebus-pro` |
| Optional `hiveram.com` commercial sync | no | yes | yes | yes | `hivebus-pro` |
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

Show build metadata:

```bash
./bin/hivebus version --json
```

## Architecture

```text
nullbot collector
  -> creates a thread and evidence bundle
  -> sends signed task.request envelopes into Hivebus

Hivebus core
  -> validates envelope structure and tier policy
  -> preserves thread state, provenance, and artifacts
  -> routes investigation work across specialist agents

investigator agents
  -> ask clarification.request when evidence is incomplete
  -> emit diagnosis.proposed only when evidence is sufficient

workledger bridge
  -> accepts only verified diagnoses
  -> creates the canonical work order that can fully resolve the user story

optional hiveram.com sync
  -> mirrors the same work order for commercial workflows when needed
```

Current code layout:

- `cmd/hivebus`: minimal CLI entrypoint
- `internal/model`: envelopes, threads, artifacts, diagnoses
- `internal/policy`: free, pro, teams, enterprise limits
- `internal/spec`: exported v0 contract and sample case bundle
- `internal/work`: deterministic workledger drafting rules with optional Hiveram sync targets

## Editions

Hivebus keeps one protocol across all editions. The differences live in policy:

- `free`: smallest retention window and artifact limits for solo experiments, implemented in this `hivebus` repo.
- `pro`: more room for production debugging and longer thread retention, implemented in `hivebus-pro`.
- `teams`: larger routing fan-out and evidence bundles for shared operations, implemented in `hivebus-pro`.
- `enterprise`: highest limits and long retention for compliance-heavy environments, implemented in `hivebus-pro`.

This keeps the protocol shared while making the repo boundary explicit: free stays open here, non-free stays out of the OSS tree.

## Canonical Workledger Contract

`workledger` is the execution source of truth for Hivebus. A verified diagnosis is not enough on its own; it must be promotable into a canonical work order with an explicit target project.

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

- This repo models the protocol and work-order gate, not the network transport yet.
- Envelope signatures are represented structurally but not cryptographically verified yet.
- There is no append-only store, replay engine, or runtime workledger/Hiveram bridge in this first cut.
- Capability routing is still declarative rather than runtime-driven.

## Roadmap

- Add append-only thread storage and deterministic replay.
- Add signed envelope verification and nonce replay protection.
- Add nullbot intake adapters and follow-up question exchange.
- Add workledger persistence and optional Hiveram sync.
- Keep non-free runtime surfaces in `hivebus-pro` instead of mixing them into this repo.
- Add queue-backed and realtime transports without changing protocol shape.

## License

MIT. See [LICENSE](LICENSE).
