# Security Policy

Hivebus is a signed channel for agent-to-agent questions. Its security model is deliberately
narrow — and stated plainly here so you can judge it for your threat model rather than infer it.

## Reporting a vulnerability

Please report security issues privately, not as public GitHub issues:

- Email **security@obstalabs.dev**, or the contact at [obstalabs.dev](https://obstalabs.dev).
- Include a description, reproduction steps, and the affected version/commit.

We aim to acknowledge within a few business days. Please give us reasonable time to release a
fix before public disclosure.

## Supported versions

Hivebus is pre-1.0 and evolves quickly. Security fixes target the **latest minor release**
(currently the `v0.3.x` line) and `main`. Older tags are not maintained — upgrade to the latest
release.

## What the signature does and does not cover

This is the honest current state, not a promise:

- **The `ask`/`answer` CLI path cryptographically verifies envelope signatures** (ed25519) and
  binds the answerer's observed world into the signed payload. See the
  [observation-context spec](docs/specs/observation-context-v0.md) for exactly what is bound and
  at which [binding level](docs/specs/observation-context-v0.md#binding-levels).
- **The runtime HTTP intake (`internal/runtime`) does not yet cryptographically verify posted
  envelope signatures.** It authenticates signed API-key tokens at the transport edge; full
  envelope-signature verification on the intake path is on the roadmap. Until then, treat the
  runtime intake surface as authenticated-but-not-signature-verified.
- **A signed answer is attributable, not automatically true.** Hivebus delivers and verifies
  provenance at the edges; it does not vouch for the *correctness* of what an answerer asserts.
  The asker checks signature, correlation, route, freshness, and observation context before
  trusting an answer — but a correctly-signed answer can still describe a stale or wrong world,
  which is why the binding level is always reported.
- **Transport security is the transport's job.** Over an SSH tunnel (see the
  [remote-ask runbook](docs/guides/remote-ask-over-ssh.md)), SSH provides confidentiality and the
  signature provides authenticity; hivebus adds no TLS of its own on that path by design.

## Scope

In scope: the protocol, signing/verification, key pinning, and the CLI/runtime in this
repository. Out of scope: out-of-tree commercial integrations
([Hiveram](https://hiveram.com), [NeuroRouter](https://neurorouter.dev),
[Bulwark](https://obstalabs.dev/bulwark)), which have their own security contacts.
