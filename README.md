[![CI](https://github.com/obstalabs/hivebus/actions/workflows/ci.yml/badge.svg)](https://github.com/obstalabs/hivebus/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8.svg)](https://go.dev)

# hivebus

A signed channel for agent-to-agent questions that binds the world the answerer observed, not just who spoke. Open source (MIT). The same dumb-but-honest bus also carries agent-native issue intake, investigation, and promotion-ready work-order creation.

## What This Is

Hivebus is a machine-native bus for agents and services that need more than raw RPC or generic queues. It treats an issue as a typed thread with structured envelopes, evidence, provenance, state transitions, and a hard gate before a work order can be created.

The product shape behind this repo is:

- `nullbot` runs close to the problem, gathers safe evidence, and opens a thread.
- specialist agents investigate asynchronously inside the same thread.
- Hivebus preserves receipts, context, and auditability as JSON-first protocol objects.
- once the diagnosis is verified, Hivebus drafts a work order for `workledger`, with optional execution integration to `hiveram.com`.
- this repo is the whole open core; live-session integration layers stay out of tree under the [boundary charter](docs/BOUNDARY.md).

## Licensing Model

This repository is the MIT open core for Hivebus. It is not a teaser for a separate paid Hivebus edition.

- this repo keeps the indispensable protocol core public and self-hostable
- protocol contracts, deterministic validation, signing, and CLI surfaces stay open here
- live-session integration layers stay out of tree under the [boundary charter](docs/BOUNDARY.md)
- new product capability does not land here by default unless a tracked work order explicitly expands the public boundary

Out-of-tree integrations may be commercial, but they do not own the protocol:

- the open-source runtime works without a license verifier
- paid or managed integrations must sit above the bus, not inside it
- no commercial layer can make envelope shapes, trust-class semantics, key pinning, or the asker/answerer CLI non-open

## Core Vs Out-of-Tree

Hivebus only becomes essential if it carries the whole path from issue intake to tracked execution. That means the open core line is not a crippled toy: it includes the protocol core and the canonical `workledger` bridge. Out-of-tree integrations can add hosted, commercial, org, and compliance layers on top.

Deployment location is not the split. Core stays self-hostable; integration-specific policy can run wherever its operator needs it. Local, Fly, VPS, and private infrastructure are all valid deployment targets.

| Capability | Open Core | Out-of-Tree Integration | Boundary |
|------------|-----------|-------------------------|----------|
| Typed JSON envelopes, threads, receipts, artifacts, and lifecycle state | yes | can extend by protocol | `hivebus` |
| NeuroRouter `nr.run.*` receipt envelope shapes for governed agent runs | yes | can consume by protocol | `hivebus` |
| Self-hosted bus core and deterministic validation/routing primitives | yes | can deploy around it | `hivebus` |
| Nullbot intake core and clarification loop | yes | can adapt around it | `hivebus` |
| Canonical `workledger` bridge: search, create, update, note, claim, release, context sync | yes | can consume by protocol | `hivebus` |
| Background analysis, selective hints, and escalation orchestration | no | yes | out-of-tree integration |
| Live-session execution or warm-context answering | no | yes | out-of-tree integration |
| Managed hosted bus/control plane | no | yes | out-of-tree integration |
| Shared queues, RBAC, team/org policy packs | no | yes | out-of-tree integration |
| Enterprise retention, BYOK, regional controls, audit exports | no | yes | out-of-tree integration |

This is the separation line: core owns the structured conversation substrate plus canonical execution tracking; out-of-tree integrations own live-session, hosted, organizational, and compliance layers that sit on top.

## What This Is NOT

- Not a human chat app with a GUI.
- Not a generic message queue replacement.
- Not an agent marketplace.
- Not autonomous remediation by default.
- Not a prompt soup relay where untyped blobs bounce between models.

Scope is governed by the [boundary charter](docs/BOUNDARY.md): Hivebus core is a dumb signed channel, and anything that makes the bus decide, generate, judge, or bridge a live session stays out of tree.

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

The point of Hivebus is that **agents can ask each other questions instead of working
blind**. One agent asks another -- "which checkout is canonical? what HEAD are you on?
are you done?" -- and gets a signed answer back, instead of crawling the other repo,
re-deriving state, or interrupting a human to relay. The bus carries the question and the
answer, verifies provenance at the edges, and decides nothing itself.

Direct ask/answer:

```text
asker agent
  -> signs a read-only query envelope (e.g. repo_status) and addresses it to another agent
  -> the bus delivers it; delivery is the whole job, a no-answer is still a success

answerer agent
  -> resolves the question from what it observes now (git read at answer time)
  -> binds the observed world into the signed payload: repo id, absolute git dir,
     device + inode, observed_at, trust class (tool_asserted vs model_inferred)

asker verification
  -> checks the signature, then the observation context: does the world this card
     describes match the repository it addressed? a wrong checkout is rejected,
     distinctly from a signature failure -- and the verification level is reported,
     never silently downgraded
```

In the open core, an answerer is a small sidecar that resolves deterministic questions
(repo state, file provenance) from disk. The deeper move -- letting a **live agent
session answer from its own warm context** ("what am I actually working on? which approach
did I pick?"), and bridging the running sessions of vendor agents into the bus -- is
live-session integration, which lives out of tree under the
[boundary charter](docs/BOUNDARY.md). The open bus makes agents talk; connecting their
live working sessions is the commercial layer ([NeuroRouter](https://neurorouter.dev) /
[Obsta Labs](https://obstalabs.dev)).

Cross-machine: a dispatched agent reports back. The most demanding case is the one where
the agent **cannot read local state and must ask over the channel** -- a dispatcher on one
machine sends an agent to a remote server, and that remote agent reports back over hivebus:
its task acknowledgement, status, and a signed result attestation. There is no shared
filesystem, so the answer is the only source of truth, and the binding-level matrix earns
its keep:

```text
dispatcher (operator machine)        remote agent (server)
  -> serve binds 127.0.0.1               -> runs under the dispatcher's gate
  -> ssh -R makes the bus reachable      -> answers/asks at localhost over the tunnel
     at the remote's localhost

remote agent
  -> signs its repo_status (or status/ack/result) over what it observes on the server

dispatcher verification
  -> checks the signature, then the observation context -- but it cannot stat the remote
     .git inode across the network, so it verifies at the REMOTE tier: the signed repo id
     and remote URL, reported as `binding_level: repo_id_only`, named not silently upgraded
```

SSH is the only wire (it already carries the dispatch); hivebus adds no transport security
of its own -- SSH is the transport, the ed25519 signature is the authenticity. The
[remote-ask-over-ssh runbook](docs/guides/remote-ask-over-ssh.md) documents this loop with a
verified two-machine transcript, and [Bulwark](https://obstalabs.dev/bulwark) places the key
material at dispatch so the first contact is pinnable rather than blind.

Open core covers the deterministic half -- signed status, task acknowledgement, and result
attestation over the bus, verified at the honest remote tier. The richer form, where the
remote agent answers from its **live warm context** rather than a disk read, is the
live-session bridge funnel, out of tree under the boundary charter.

Issue intake to tracked work (the coordination surface):

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

optional execution integration
  -> mirrors or extends the same work order outside the core runtime when needed
```

Current code layout:

- `cmd/hivebus`: minimal CLI entrypoint
- `internal/cli`: the `serve`, `ask`, `answer`, and `watch` commands; the ask/answer
  resolvers, answer-key pinning, and observation-context verification
- `internal/model`: envelopes, threads, artifacts, diagnoses, recovery capsules, signing
- `internal/runtime`: v0 HTTP handlers for intake, promotion, dispatch, agent sessions,
  inbox/delivery, thread creation, append, and replay
- `internal/policy`: free, pro, teams, enterprise limits
- `internal/spec`: exported v0 contract and sample case bundle
- `internal/store`: SQLite append-only event log, deterministic replay, and verified recovery projections
- `internal/work`: deterministic workledger drafting rules with optional Hiveram execution targets

## Editions

Hivebus keeps one protocol across policy tiers. The labels in code are local limits and coordination profiles, not a promise of separate paid Hivebus repos or protocol forks:

- `free`: self-hostable protocol core, smallest retention window, and artifact limits for solo experiments, implemented in this `hivebus` repo.
- `pro`: single-tenant production coordination profile with longer retention and higher local limits when an out-of-tree integration enables it.
- `teams`: shared coordination profile with multi-operator policy and larger routing/evidence limits when an out-of-tree integration enables it.
- `enterprise`: corporate controls, compliance posture, and governance profile when an out-of-tree integration enables it.

This keeps the protocol shared while making the repo boundary explicit: core stays open here, integration-specific behavior stays out of the OSS tree, and no policy tier requires a specific hosting location.

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

Out-of-tree execution integrations may mirror or present the same work order, but they do not replace `workledger` as the ledger of record.

Background analysis and live-session orchestration are out of tree. They can consume the same protocol, but they do not belong inside core.

## Known Limitations

- The v0 runtime is HTTP-only and intentionally small: nullbot intake, thread promotion, dispatch, append, and replay.
- Runtime HTTP intake (`internal/runtime`) authenticates signed API-key tokens but does not yet cryptographically verify posted envelope signatures. The `ask`/`answer` CLI path does verify them (see [docs/specs/observation-context-v0.md](docs/specs/observation-context-v0.md)).
- The workledger bridge requires explicit `WORKLEDGER_URL` or `WORKLEDGER_HOST` plus `WORKLEDGER_API_KEY` configuration on the runtime host.
- Optional execution sync is exposed as a hook surface, not a bundled core-runtime integration.
- Capability routing is still declarative rather than runtime-driven.
- Runtime auth validates Ed25519-signed API keys locally, with hashed token files retained only as an explicit local fallback.

## Roadmap

- Add signed envelope verification and nonce replay protection.
- Add additional nullbot intake adapters beyond the v0 HTTP path.
- Extend the workledger bridge with richer search/update/note flows while keeping optional execution integrations out of tree.
- Keep smart-bus and live-session runtime surfaces out of this repo.
- Add queue-backed and realtime transports without changing protocol shape.

## License

MIT. See [LICENSE](LICENSE).
