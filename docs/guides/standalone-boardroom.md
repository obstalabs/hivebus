# Standalone Hivebus Boardroom

Hivebus can be the local boardroom binary for processes that do not run under
NeuroRouter. A Codex session, shell script, test harness, or other local process can
send and receive messages through the public Hivebus runtime with no NR-private
protocol fields.

Use this when you need a simple local coordination channel. Use `hivebus ask` when
the exchange is a bounded signed question that expects a verifiable answer.

## Local Loop

Start a local runtime. This example disables auth because the listener is loopback
and intended for single-user local development:

```bash
hivebus serve --auth-disabled --listen 127.0.0.1:7097 --db /tmp/hivebus-boardroom.db
```

Register a participant and wait for messages:

```bash
hivebus listen --server http://127.0.0.1:7097 --insecure \
  --session-id codex-1 \
  --participant codex/hivebus \
  --ack
```

Send a message from another process:

```bash
echo "please read /tmp/handshake.md and ack" | hivebus say \
  --server http://127.0.0.1:7097 --insecure \
  --from architect/hivebus \
  --session-id architect-1 \
  --to codex/hivebus
```

Read an inbox once instead of waiting:

```bash
hivebus inbox --server http://127.0.0.1:7097 --insecure \
  --session-id codex-1 \
  --ack
```

`listen` reports `message`, `timeout`, or `bus_down` distinctly. `inbox --ack` and
`listen --ack` mark returned messages delivered so repeated reads do not show the same
item again.

## Authenticated Runtime

For an authenticated runtime, omit `--insecure` and provide the runtime's worker and
operator credentials by reference from your caller environment:

```bash
hivebus say --server http://127.0.0.1:7097 \
  --operator-token "$HIVEBUS_OPERATOR_BEARER" \
  --worker-token "$HIVEBUS_WORKER_BEARER" \
  --from architect/hivebus \
  --session-id architect-1 \
  --to codex/hivebus \
  "status?"

hivebus listen --server http://127.0.0.1:7097 \
  --worker-token "$HIVEBUS_WORKER_BEARER" \
  --session-id codex-1 \
  --participant codex/hivebus \
  --ack
```

Do not paste credential values into docs, logs, or issue comments. Pass them through the
calling environment or a local secret manager.

## Command Roles

- `hivebus say` registers the sender session, then queues one bounded text message to a
target participant.
- `hivebus inbox` fetches queued messages for one session and can acknowledge them with
`--ack`.
- `hivebus listen` repeats the inbox read until a message arrives, the timeout expires,
or the runtime is unreachable.
- `hivebus ask` remains the signed question/answer path. It uses the same runtime but
wraps the message body in signed query and answer envelopes.

## Embedding And Conformance Boundary

PR #18 adds two public surfaces that keep Hivebus as the protocol owner:

- `github.com/obstalabs/hivebus/embed` lets another Go process serve the Hivebus runtime
over a caller-owned listener. The embedder owns process lifecycle and local IPC; Hivebus
owns the `/v0/agents/*` runtime semantics.
- `github.com/obstalabs/hivebus/conformance` exposes importable golden JSON fixtures for
the agent wire contract. External consumers run the same fixture comparisons in their own
CI instead of copying private wire structs.

The boundary is deliberate: Hivebus owns transport, presence, inbox, delivery, and public
wire compatibility. NeuroRouter or another integration may wrap this binary or embed the
runtime, but governance concepts such as posture, authority, directives, and live-session
policy stay outside the public bus layer.
