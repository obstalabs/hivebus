# Contributing

## Development Loop

1. Run `make build` after structural changes.
2. Run targeted tests while iterating.
3. Run `make test`, `make vet`, and `make lint` before opening a PR.

## Design Rules

- Keep the protocol typed and transport-agnostic.
- Add tests for every new behavior.
- Preserve the WO gate: verified diagnosis first, execution second.
- Keep live-session integration and smart-bus behavior out of this repo.

## Scope check before you build

Before starting a PR, ask one question: does this change make Hivebus decide, generate, judge, or bridge a live session? If yes, it belongs out of tree under [docs/BOUNDARY.md](docs/BOUNDARY.md).

If the change only hardens the dumb signed channel, protocol contracts, deterministic resolvers, signing, key pinning, trust classes, or the asker/answerer CLI, it is inside the public boundary. Out-of-scope PRs are closed with a pointer to the charter, not debated case by case.

## Repository Boundary

This repo follows the `neurorouter-free` model: it is the community/core edition.

Allowed by default:

- protocol hardening, validation, and deterministic routing behavior
- workledger bridge maintenance and compatibility
- nullbot intake core and other existing community-surface behavior
- tests, docs, CI, release, and packaging hygiene

Not allowed by default:

- live-session or commercial execution sync
- hosted control-plane behavior
- team/org features such as RBAC or shared queues
- enterprise-only compliance and data residency surfaces
- key-gated or paid-only product capability

When in doubt, assume the change belongs out of tree until a tracked work order explicitly expands the community boundary.

## Commit Style

Use conventional commits such as `feat: add signed envelope verification`.
