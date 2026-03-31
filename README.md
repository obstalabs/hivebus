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

- `free`: smallest retention window and artifact limits for solo experiments.
- `pro`: more room for production debugging and longer thread retention.
- `teams`: larger routing fan-out and evidence bundles for shared operations.
- `enterprise`: highest limits and long retention for compliance-heavy environments.

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
- Add queue-backed and realtime transports without changing protocol shape.

## License

MIT. See [LICENSE](LICENSE).
