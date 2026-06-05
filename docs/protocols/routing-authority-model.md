# Hivebus Routing + Authority Model (WO-81 research)

Status: recommendation (research-only; no delivery implementation in this WO)
Builds on: `internal/model/message.go` (Envelope), `internal/policy/tiers.go` (4-tier policy), WO-87 signed-envelope substrate.

## The question

The signed-pipe messaging idea has four orthogonal axes: **visibility** (inband/offband),
**scope** (targeted/broadcast), **signing** (required for ether), and **direction/reply-routing**.
The first three are envelope fields. Direction is not — it is an *authority* model. This report
settles direction's authority semantics so the schema can be frozen without freezing the wrong primitive.

## Core finding: direction is DERIVED, not an enum

Direction must NOT be a four-value enum (`one-way | bidirectional | redirect | broadcast`).
An enum forces every future mode to be a code change and hides the authority questions inside a label.
Instead, the named modes **emerge** from composing orthogonal fields:

| Field | Values | Meaning |
|-------|--------|---------|
| `visibility` | `inband` \| `offband` | which pipe (the WO already-shipped axis) |
| `scope` | `targeted` \| `broadcast` | one recipient vs many |
| `recipient` | agent_id \| channel_id \| null | null iff broadcast |
| `reply_to` | message_id \| null | correlation to a prior message (exists today as `ReplyTo`) |
| `reply_policy` | `none` \| `reply_to_sender` \| `reply_to_target` \| `collect` | where replies go |
| `reply_target` | agent_id \| channel_id \| null | the C in reply-redirect; required iff `reply_to_target` |
| `collection_policy` | `none` \| `first` \| `all_until_timeout` \| `quorum` \| `manual_review` | broadcast-collect aggregation |

Named modes are then derived, never stored:

- **one-way** = `reply_policy:none`
- **bidirectional** = `reply_policy:reply_to_sender`
- **reply-redirect** = `reply_policy:reply_to_target` + `reply_target:C`
- **broadcast-collect** = `scope:broadcast` + `reply_policy:collect` + `collection_policy:all_until_timeout`
- **broadcast-announce** = `scope:broadcast` + `reply_policy:none`

Reasoning: every authority question below attaches to a *field*, not to a mode. If direction were an
enum, "who may redirect to C" would have nowhere to live except special-cased per enum value. Composable
fields put each authority rule exactly where it is enforced.

## The seven authority questions, answered

### Q1. Who may redirect replies to C? Must C consent?

A redirect (`reply_policy:reply_to_target`, `reply_target:C`) is an **instruction the original sender A
issues to B**: "when you answer, send the answer to C instead of to me." The authority rule:

- **A may always *request* a redirect** — it is A's own message; A may declare where A wants replies to go.
- **C is NOT obligated to accept** the redirected reply. Redirect is a routing *request*, not a delivery
  guarantee. This is the propose-don't-mutate canon applied to routing: A proposes the route; the
  receiving side disposes.
- **Consent model**: C consents by **subscription/acceptance posture**, not per-message handshake.
  - If C has an open inbox for this thread/channel (C is a participant), the redirected reply is delivered — C consented by participating.
  - If C is NOT a participant, the redirected reply lands in C's **pending/quarantine** state and is delivered only if C's policy accepts unsolicited inbound. Default-deny.
  - This is the off-band-consent red thread: **C consents before receiving redirected replies** by its
    standing acceptance posture; A cannot force delivery into C.

This keeps redirect useful (the common case — C is a participant, e.g. the architect session collecting
fleet replies — just works) while making spam structurally hard (C-as-stranger is default-deny).

### Q2. Can B see that the reply is redirected? (transparency)

**Yes — mandatory.** `reply_policy` and `reply_target` are signed envelope fields (covered by WO-87's
canonical bytes). B receives A's message with the redirect visible and signed. B always knows it is
answering to C, not A. **No silent redirects** — a redirect B cannot see would be an exfiltration
primitive (A tricks B into sending data to C while B believes it answers A). Transparency is the defense.

### Q3. Can a malicious sender use redirect to spam or impersonate C?

Two distinct attacks, two structural defenses:

- **Spam C** (A redirects many B-replies at C): defended by Q1's default-deny — C only receives
  redirected replies it has a standing acceptance posture for. A flooding strangers' replies at C hits
  C's quarantine, not C's inbox.
- **Impersonate C** (A sets `from:C` or forges C as the answerer): defended by WO-87 signatures. The
  *answer* B produces is signed by **B**, not A and not C. A cannot forge B's signature; B cannot be made
  to appear as C. `reply_target:C` only says "deliver to C" — it never says "from C." Authenticity of the
  answerer is the signer, always.

### Q4. Who may answer a broadcast? Can replies be offband when the original was inband?

- **Who may answer**: any agent that *receives* the broadcast (is subscribed to the channel/scope). A
  broadcast is an open call; answering is voluntary. The collector (Q5) decides which answers it keeps.
- **Offband reply to an inband original**: **allowed, and signed.** Visibility is per-message, not
  per-thread — a reply carries its own `visibility`. An agent may answer an inband broadcast with an
  offband reply (e.g. the content is sensitive). The correlation (`reply_to`) ties them; the visibility
  may differ. This is *why* visibility is an orthogonal field and not a thread property.

### Q5. Does broadcast-collect create an implicit inbox/queue?

**Yes — and this is the line between schema and delivery.** `collection_policy` *describes* the
aggregation intent (first / all-until-timeout / quorum / manual-review), but actually *holding* replies,
timing them out, and deduping them requires a **collector with state** — a queue. That queue is
**delivery-side (Pro/tier-gated)**, not schema. The envelope can *describe* `collect` on any tier; only a
tier with the collector may *deliver* it. (See core/pro split below.)

### Q6. How are replies correlated, threaded, expired, deduped, audited?

- **Correlated**: `reply_to` = the message_id being answered (exists today). `ThreadID` groups the
  exchange.
- **Threaded**: existing `ThreadID` + `reply_to` chain. No new primitive needed.
- **Expired**: `Deadline` (exists today) bounds how long a collector waits; `all_until_timeout` reads it.
- **Deduped**: `IdempotencyKey` (exists today) — a resent answer with the same key is one answer.
- **Audited**: every envelope is signed (WO-87) and append-only in the thread store. The redirect and the
  answerer's signature are both in the permanent record. Audit is a property of the signed log, not a new field.

The substrate already carries every correlation primitive. **No new correlation field is needed** — only
`reply_policy`/`reply_target`/`collection_policy` and `visibility`/`scope`/`recipient`.

### Q7. What signature/authenticity is required for a redirected or broadcast reply?

Same as any ether message: **signed by the answerer** (WO-87 `SignEnvelope`/`VerifyEnvelope`). A redirected
reply is not special — it is an answer signed by B, addressed to C, correlated to A's message. A broadcast
reply is signed by the answerer, correlated to the broadcast. The signature authenticates *who answered*;
the routing fields say *where it goes*. These never conflate.

## Q8. The minimal first shippable CLI slice

**`stdin → signed envelope → targeted one-way delivery.`** Concretely:

```
echo '<payload>' | hivebus send --to <agent> --type <type>   # signs, delivers one-way, no reply
```

This needs only: `recipient` (targeted), `reply_policy:none`, `visibility:inband`, WO-87 signing. It is
the smallest thing that proves the pipe. Then increment, each a separate WO:

1. `--offband` (visibility)
2. ask/answer (`reply_policy:reply_to_sender`) — **this is WO-84, and per the finding below it needs NO routing fields**
3. `--broadcast` (scope) + announce (`reply_policy:none`)
4. `reply_to_target` redirect (Q1 consent model)
5. `collect` + collector queue (Pro)

## Core / Pro split invariant

**The envelope must be able to DESCRIBE any route+authority, even where a tier may not DELIVER it.**

- **Schema = what a message IS** → neutral substrate, MIT core. All routing *fields* (`visibility`,
  `scope`, `recipient`, `reply_policy`, `reply_target`, `collection_policy`) live in `internal/model` and
  parse on every tier. Any tier can read any route.
- **Delivery = what the system is ALLOWED to do** → Pro/tier-gated. The collector queue (Q5), broadcast
  fan-out, and offband transport are delivery capabilities gated in `internal/policy/tiers.go`.

**Recommended line**: targeted one-way + ask/answer (reply_to_sender) deliver on **all tiers** (the free
core is genuinely useful — the architect can ask the fleet). Broadcast-collect, reply-redirect, and
offband delivery are **Pro+** (they need the stateful collector / fan-out / off-band pipe that is the
product value). The schema describing them stays MIT.

## The unblock finding (highest-value result for the roadmap)

**ask/answer (WO-84) does NOT need the WO-82 routing fields.** It composes entirely from substrate that is
already on `main`:

- `query` / `answer` message types — shipped (WO-87)
- `To` (recipient), `ReplyTo` (correlation), `ThreadID` — exist today
- sign/verify — shipped (WO-87)
- mode = bidirectional = `reply_policy:reply_to_sender`, which is the **default** (reply goes to sender) —
  needs no stored field; "answer goes back to whoever signed the query" is the natural default.

Therefore the dependency `WO-84 → WO-82` can be **dropped**. WO-84 depends only on WO-87 (done). The
"stupid demo" — ask the workledger agent "which checkout is canonical?", get one signed answer — is
buildable **now**, on shipped substrate, with no routing-field work and no WO-81-gated decisions.

WO-82 (routing fields) remains the home for the *non-default* policies (redirect, broadcast, collect)
and stays correctly downstream of this research.

## Proposed child specs

1. **(exists) WO-82** — schema: add `visibility`/`scope`/`recipient`/`reply_policy`/`reply_target`/
   `collection_policy` as orthogonal signed fields + coherence validation. Unblocked by this report.
   Acceptance per this model: direction is DERIVED; coherence rules (reply_target required iff
   reply_to_target; recipient null iff broadcast; collection_policy only with collect; signed for ether).
2. **(new) ask/answer delivery, narrow** — `reply_policy:reply_to_sender` default on substrate; the
   `hivebus send`/`hivebus ask` one-way + ask/answer CLI slice. NOT blocked on WO-82. (This is WO-84
   re-scoped: drop the WO-82 dependency.)
3. **(new, Pro) collector + broadcast delivery** — the stateful queue for `collect`, broadcast fan-out,
   offband transport. Tier-gated. Depends on WO-82 schema.
4. **(new) reply-redirect consent enforcement** — the Q1 default-deny acceptance-posture check for
   `reply_to_target` to a non-participant C. Depends on WO-82 schema.

## Decisions, summarized

- Direction is **derived from composable fields**, not an enum.
- Redirect is a **request**; C consents by **standing acceptance posture** (default-deny for strangers).
- Redirects are **always transparent and signed** (no silent redirect).
- Answerer authenticity is **always the signer** (redirect never implies from).
- Visibility is **per-message** (offband reply to inband original is legal).
- Correlation/expiry/dedup/audit reuse **existing** fields (ReplyTo/Deadline/IdempotencyKey/signature) — no new correlation primitive.
- Schema (all routing fields) is **MIT core**; stateful delivery (collector/broadcast/offband) is **Pro**.
- **ask/answer is unblocked from WO-82** and shippable on current `main`.
