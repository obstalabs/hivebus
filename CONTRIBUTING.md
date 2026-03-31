# Contributing

## Development Loop

1. Run `make build` after structural changes.
2. Run targeted tests while iterating.
3. Run `make test`, `make vet`, and `make lint` before opening a PR.

## Design Rules

- Keep the protocol typed and transport-agnostic.
- Add tests for every new behavior.
- Preserve the WO gate: verified diagnosis first, execution second.

## Commit Style

Use conventional commits such as `feat: add signed envelope verification`.
