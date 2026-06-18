# Runbook: a fleet of agents asking each other what they're working on

Several agents share one machine — say a fleet of Claude sessions on different work
orders. One needs to know what another is doing: *"which WO is that session on?"* Instead
of a human relaying the answer between two terminals, the agents ask each other over
hivebus and get a **signed** answer back.

Each agent plays both roles: it **answers** questions about its own repo (`repo_status`),
and it **asks** other agents about theirs. One bus carries all of it.

This builds on the [local ask/answer loop](local-ask-answer.md); the only new idea is that
many agents join the same bus at once. The `scripts/hivebus-fleet` wrapper keeps the
per-agent setup to one line.

## The model

```text
            one bus (serve, loopback)
        /          |            \
  sonnet         codex         qwen        each runs `answer` for its own repo,
  (WO-216)      (WO-219)      (WO-221)      declaring its work order with --wo
        \          |            /
         each also runs `ask` to query the others
```

- An agent's `answer` loop responds to `repo_status` about **its own** checkout: repo,
  branch, HEAD, dirty — and the work order it declared with `--wo`.
- Any agent's `ask` addresses a **named** peer and gets that peer's signed answer.
- The answer is signed and observation-context-bound, so the asker trusts it without
  crawling the other repo. A signed answer is attributable, not automatically true.

## Setup with `scripts/hivebus-fleet`

The wrapper is a thin shell over the `hivebus` CLI — no extra protocol. `hivebus` must be
on `PATH` (`go install …`, `brew install obstalabs/tap/hivebus`, or `make build` then point
`HIVEBUS_BIN` at `./bin/hivebus`).

```sh
# Once, on the shared machine — the bus:
scripts/hivebus-fleet bus
# (loopback 127.0.0.1:7097; override with HIVEBUS_FLEET_LISTEN / _SERVER / _DB)

# Each agent session joins, naming itself, its repo, and its work order:
scripts/hivebus-fleet join freeflow/sonnet ~/src/checkout-api WO-216 &
scripts/hivebus-fleet join freeflow/codex  ~/src/checkout-api WO-219 &

# Any agent asks a named peer what it's working on:
scripts/hivebus-fleet ask freeflow/sonnet
```

`ask` prints the signed answer; the `wo` field is the work order the peer declared, and
`branch` corroborates it:

```text
delivered: true   answers: 1   response_status: answered
  ... "wo": "WO-216", "branch": "wo-216/...", "binding_level": "git_dir_inode" ...
```

`join` gives each agent a stable signing key under `~/.hivebus/<agent>.seed`, so its public
key does not change across restarts. The first `ask` pins that key in
`~/.hivebus/known_answerers` and prints its fingerprint; a later key swap is rejected as a
possible impersonation (the SSH `known_hosts` model — see
[local-ask-answer.md](local-ask-answer.md#known-answerer-pins)).

## Doing it by hand (no wrapper)

The wrapper is optional. The same loop with the raw CLI:

```sh
# bus
hivebus serve --auth-disabled --listen 127.0.0.1:7097 --db ~/.hivebus/fleet.db

# an agent answers for its repo, declaring its WO
hivebus answer --server http://127.0.0.1:7097 --insecure \
  --agent freeflow/sonnet --session-id sonnet-1 \
  --signing-key "$(cat ~/.hivebus/sonnet.seed)" \
  --repo ~/src/checkout-api --project fleet --wo WO-216

# another agent asks it
echo "what work order are you on?" | hivebus ask \
  --server http://127.0.0.1:7097 --insecure \
  --to freeflow/sonnet --from freeflow/codex --type repo_status \
  --repo ~/src/checkout-api --session-id codex-1 --timeout 6s
```

## Across machines

If the agents are on different hosts, the bus stays loopback on one host and the others
reach it over an SSH reverse tunnel — same commands, different wire. See
[remote-ask-over-ssh.md](remote-ask-over-ssh.md). Across machines the answer verifies at the
`repo_id_only` tier (the asker cannot stat the peer's inode), reported, never inflated.

## Known limitation — you must know *which* agent to ask

Today `ask` addresses a **named** agent. There is no roster broadcast yet, so the literal
question *"who is working on WO-216?"* — asked of the whole fleet at once — is not a single
command. You ask each agent you know about and read its `wo`. Asking an agent that never
joined fails plainly:

```text
Error: answer public key for agent freeflow/qwen is not available from bus session record
```

Two pieces make the broadcast first-class, and are the next build:

- a **roster** (`GET /v0/agents/sessions`) so an agent can list who is on the bus and fan
  out the question;
- a **`work_status`** resolver so an agent answers "which WO do I hold" as a typed claim
  rather than the asker inferring it from `repo_status`.

Until then, fleet coordination is point-to-point between agents that know each other's
names — which is enough for a small, named fleet.

## The boundary

hivebus delivers and verifies provenance; it does not decide who should be working on what,
or resolve conflicts. It carries the signed question and the signed answer. What the agents
do with the answer — pause, hand off, escalate — is the agents' job, not the channel's.
