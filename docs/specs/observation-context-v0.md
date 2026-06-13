# Observation-Context Binding — v0

**Version:** v0
**Date:** 2026-06-13
**License:** MIT (same as the repository)
**Status:** Describes behavior shipping in `internal/cli` as of this date. Sections marked
*specified, not yet enforced* describe the intended extension and are called out explicitly
so this document records reality, not aspiration.

## What this document is

A precise, citable description of how Hivebus binds **the world an answerer observed** into
its signed answer — not merely *who* signed it. A validly-signed answer can be fresh,
attributable, and still describe the wrong repository; observation-context binding is the
mechanism that catches that at verification time.

This is the canonical reference for the one property that distinguishes Hivebus from
identity-only agent-to-agent signing: the signature covers the observed git directory,
device, and inode, and a wrong checkout is rejected **distinctly** from a signature failure.

Scope boundary: this document covers the `ask`/`answer` CLI protocol surface
(`internal/cli`). It does not describe the runtime HTTP intake surface (`internal/runtime`),
whose current signature posture is noted under [Surface scope](#surface-scope-cli-vs-runtime).
The dumb-channel boundary that governs what may and may not live in the core is
[docs/BOUNDARY.md](../BOUNDARY.md).

## The signed repo_status card

When an answerer resolves a `repo_status` question, it emits a signed answer envelope whose
payload is the `repoStatusAnswerPayload`
(`internal/cli/answer.go:144`). The fields, as actually emitted:

| Field | JSON key | Source |
|-------|----------|--------|
| Question type | `question_type` | `answer.go:145` — echoes the asked type |
| Read-only flag | `read_only` | `answer.go:146` — repo_status is always read-only |
| Trust class | `trust_class` | `answer.go:147`, set to `tool_asserted` at `answer.go:642` |
| Agent ID | `agent_id` | `answer.go:148` |
| Project | `project` | `answer.go:149` |
| Repo ID | `repo_id` | `answer.go:150` — the addressed worktree identity (WO-119) |
| Canonical repo path | `canonical_repo_path` | `answer.go:151` |
| Remote | `remote` | `answer.go:152` |
| Branch | `branch` | `answer.go:153` |
| Head SHA | `git_head_sha` | `answer.go:154` |
| Head (short/full) | `head.short` / `head.full` | `answer.go:155`–`158` |
| **Absolute git dir** | `absolute_git_dir` | `answer.go:159` — the observed `.git` directory (WO-119) |
| **Git dir device** | `git_dir_dev` | `answer.go:160` — inode fingerprint (WO-119) |
| **Git dir inode** | `git_dir_ino` | `answer.go:161` — inode fingerprint (WO-119) |
| Dirty | `dirty` | `answer.go:162` |
| Worktree role | `worktree_role` | `answer.go:163` |
| Lease | `lease` | `answer.go:164` (omitempty) |
| Work order | `wo` | `answer.go:165` (omitempty) |
| Observed at | `observed_at` | `answer.go:166` |
| Expires at | `expires_at` | `answer.go:167` |
| Nonce | `nonce` | `answer.go:168` |

The three **bold** fields are the observation-context binding. They are read from disk at
answer time, not from registration metadata: `absolute_git_dir` is produced by
`git rev-parse --absolute-git-dir`, and `git_dir_dev`/`git_dir_ino` by `os.Stat` on that
directory (`gitDirDeviceInode`, `answer.go:1084`).

### Freshness

`observed_at` is the answerer's clock at resolve time; `expires_at` is `observed_at` plus the
answer TTL (`answer.go:484`–`485`). The default TTL is **30 seconds** (`defaultAnswerTTL`,
`answer.go:30`). A repo_status card is a point-in-time observation, not a durable fact.

## The verification algorithm

The asker verifies a returned answer in a fixed order (`internal/cli/ask.go`,
`validateLiveAskAnswerForRepo` at `ask.go:796`):

1. **Envelope shape** — type is `answer`, `reply_to` matches the query message ID,
   `thread_id` matches (`ask.go:808`).
2. **Route binding** — the answer comes from the addressed target
   (`validateLiveAskAnswerRoute`, `ask.go:813`).
3. **Signature** — `model.VerifyEnvelope(answer, answerPublicKey)`; failure returns
   `answer signature verification failed` (`ask.go:816`).
4. **Deadline** — if the envelope carries a deadline and it has passed, reject
   (`ask.go:819`).
5. **Observation context** — `validateLiveAskRepoStatusObservation` (`ask.go:823`, defined
   at `ask.go:826`), which returns the **binding level** that actually verified plus the
   **remote-fingerprint** outcome.

Steps 3 and 5 are distinct: a bad signature fails at step 3 with a signature error; a
correctly-signed answer that observed the *wrong world* fails at step 5 with an
observation error. That distinction is the whole point.

### Step 5 in detail

For a `repo_status` answer (`validateLiveAskRepoStatusObservation`, `ask.go:826`):

- The payload must parse as JSON (`ask.go:851`).
- `trust_class` must equal `tool_asserted`, else reject (`ask.go:853`). See
  [Trust classes](#trust-classes).
- `read_only` must be true (`ask.go:856`).
- `question_type` must be `repo_status` (`ask.go:859`).
- `repo_id` must be present and **must equal the addressed repo ID** — a mismatch is
  `answer observed wrong repo` (`ask.go:862`–`865`).
- `git_head_sha` must be present and, if `head.full` is set, must equal it
  (`ask.go:868`–`871`).
- `absolute_git_dir` must be present (`ask.go:874`).
- `observed_at` and `expires_at` must be present; `now` must not be after `expires_at`,
  else `repo_status observation is expired` (`ask.go:877`–`883`).
- **Remote-fingerprint anchor** — `verifyRepoStatusRemote` (`ask.go:920`) checks the signed
  `remote` against an expectation that is either operator-asserted (`--expect-remote`) or
  derived from a local clone's `origin` (`addressedRepoOrigin`, `ask.go:944`). A mismatch is
  a distinct `answer observed wrong remote` error; see [Binding levels](#binding-levels).
- The asker resolves the **addressed** repo's own git identity locally
  (`addressedRepoGitIdentity`, `ask.go:894`) and compares, naming the level it reached:
  - repo not resolvable locally → level `repo_id_only` (`ask.go:895`–`896`).
  - canonical `absolute_git_dir` must match, else `answer observed wrong git dir`
    (`ask.go:899`).
  - inode unavailable (zero) but path matched → level `git_dir_path` (`ask.go:902`).
  - `git_dir_dev`/`git_dir_ino` must match, else `answer observed wrong git dir inode`
    (`ask.go:905`); on match → level `git_dir_inode` (`ask.go:914`).

A wrong checkout sharing a plausible path is therefore caught by the device+inode
comparison even when the path string looks right.

## Binding levels

Observation context is verifiable to different strengths depending on where the asker and
answerer sit relative to each other. The level reached **is named on every verify, never
silently treated as the strongest tier** (WO-123). A successful `repo_status` ask reports
its `binding_level` and `remote_check` in the ask output (`askExchange`, `ask.go:91`–`92`).

| Level | `binding_level` | Binding | Status |
|-------|-----------------|---------|--------|
| **Same host** | `git_dir_inode` | device + inode of the `.git` directory match (`ask.go:905`, named `ask.go:914`) | **Enforced today** |
| Same host, path only | `git_dir_path` | canonical `absolute_git_dir` matches, inode unavailable (`ask.go:899`, named `ask.go:902`) | **Enforced today** |
| Remote / cross-machine | `repo_id_only` + `remote_check` | repo not resolvable locally; `repo_id` string compare anchored by the remote-URL fingerprint (`ask.go:896`, `verifyRepoStatusRemote` `ask.go:920`) | **Enforced today** |
| Cross-org | — | signed delegation across trust domains | **Specified, not yet enforced** |

### No silent downgrade

Earlier revisions of this spec documented two best-effort downgrades that *passed at a
weaker level without naming it.* WO-123 closed that: every verify outcome now returns an
`observationVerification` (`ask.go:102`) carrying the achieved `binding_level`, so a weaker
result is explicit rather than invisible:

- repo not resolvable locally → `binding_level=repo_id_only` (`ask.go:895`–`896`), not a
  silent success.
- inode unavailable but path matched → `binding_level=git_dir_path` (`ask.go:902`).

The remote tier is now anchored, not string-only: `verifyRepoStatusRemote` (`ask.go:920`)
compares the signed `remote` against an operator-asserted `--expect-remote` or a
locally-derived clone `origin`. A mismatch is rejected distinctly (`answer observed wrong
remote`, `ask.go:935`/`938`); an absent expectation is reported as `remote_check=unavailable`
(`ask.go:929`), never silently treated as a match. URL comparison is structural-only
normalization (`normalizeRemoteURL`, `ask.go:960`): scheme and `.git`-suffix variants of the
same repo are equated, but distinct hosts, orgs, and case are not.

The remaining *specified, not yet enforced* row is cross-org signed delegation across trust
domains; this document will be revised when that lands.

## Trust classes

`trust_class` separates deterministic facts from model judgments so that a fact never
launders into an inference:

- `tool_asserted` — the value was read by a tool (here, git on disk). This is the only
  class a `repo_status` answer may carry; the answerer sets it at `answer.go:642` and the
  verifier rejects anything else at `ask.go:853`.
- `model_inferred` — **specified, not emitted.** It is the reserved counterpart for answers
  that are a model's judgment rather than a tool reading. No resolver in `internal/cli`
  emits it today, and the repo_status verifier would reject it. It is documented here so the
  field's contract is explicit: a model's restatement of a fact is an inference, not a
  `tool_asserted` fact, and must not be relabeled to pass verification.

## The signing envelope

Signatures are ed25519 over canonical envelope bytes
(`internal/model/envelope_signing.go`):

- `CanonicalEnvelopeBytes` marshals the envelope with the signature field cleared
  (`envelope_signing.go:16`), so the signature covers every other field — including the
  full observation-context payload.
- `SignEnvelope` sets `security.signed = true`, then signs the canonical bytes
  (`envelope_signing.go:36`–`43`).
- `VerifyEnvelope` requires `security.scheme == ed25519`, requires a signature, re-derives
  the canonical bytes, and checks `ed25519.Verify` (`envelope_signing.go:49`–`83`).

Because the canonical bytes include `absolute_git_dir`, `git_dir_dev`, and `git_dir_ino`,
the observation context is **cryptographically bound** into the answer; it cannot be edited
after signing without invalidating the signature.

## Surface scope: CLI vs runtime

This specification describes the `ask`/`answer` CLI protocol surface (`internal/cli`),
where envelope signatures **are** cryptographically verified (`ask.go:489` for queries,
`ask.go:816` for answers).

The repository's README "Known Limitations" note that "envelope signatures are represented
structurally but not cryptographically verified yet" refers to the **runtime HTTP intake
surface** (`internal/runtime`), which today authenticates signed API-key tokens
(`internal/runtime/auth.go:192`) but does not yet run `VerifyEnvelope` over posted message
payloads. The two surfaces are at different stages; this document is scoped to the verified
CLI path.

## References

- Boundary charter: [docs/BOUNDARY.md](../BOUNDARY.md) — the dumb-channel core boundary.
- Binding implementation: `internal/cli/answer.go`, `internal/cli/ask.go`.
- Signing: `internal/model/envelope_signing.go`.
- Binding-level transparency and remote-fingerprint binding: WO-123.
- Origin of the observation-context fields: WO-119.
