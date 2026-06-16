# Runbook: hivebus over an SSH reverse tunnel

Run the hivebus ask/answer nervous system across **two machines** — an operator box and a
remote worker — with **SSH as the only wire**. The bus (`serve`) binds loopback on the
operator; an `ssh -R` reverse tunnel makes that loopback port reachable as `localhost` on the
remote. hivebus adds no transport security of its own here: **SSH is the transport, the
ed25519 signature is the authenticity**, and both ends stay loopback-only.

This pairs with the local loop in [local-ask-answer.md](local-ask-answer.md) — same commands,
same shells (**fish**, **bash**, **sh/zsh**); the only new thing is the tunnel.

This runbook covers **both directions**:

- **Direction A — the remote reports back.** The remote worker runs `answer`; the operator
  runs `ask`. "A dispatched agent phones its dispatcher": the operator asks the remote for its
  signed `repo_status`. This is the dispatch direction.
- **Direction B — the remote asks the operator.** The remote worker runs `ask`; the operator
  runs `answer`. The remote consults an operator-side agent over the same tunnel.

## What success looks like

```
delivered: true   answers: 1   response_status: answered
```

For a cross-machine `repo_status` answer where the **asker has no local clone of the
addressed repo** (the normal remote case), the verified result reports:

```
binding_level: repo_id_only
```

This is honest and expected: the asker cannot stat the answerer's `.git` inode across the
network, so it verifies the signature, freshness, route, and the signed `repo_id` string —
the remote binding tier — rather than the stronger same-host `git_dir_inode` tier. The level
is always named, never silently upgraded. (See
[observation-context-v0.md](../specs/observation-context-v0.md) for the binding levels.)

`delivered: true / answers: 0 / response_status: no_answer` is **also a channel success** — the
query was delivered but no agent answered. hivebus is the ether, not the mind.

---

## 0. Build (both machines)

```sh
cd ~/dev/obstalabs-github/hivebus
make build           # produces bin/hivebus
# optional: install on PATH
sudo cp bin/hivebus /usr/local/bin/
```

`hivebus` must be on PATH (or use `./bin/hivebus`) on **both** the operator and the remote.

---

## 1. Keys: the manual bootstrap (and its automated future)

The answerer signs every answer with a stable 32-byte base64 seed; the asker pins the
answerer's public key on first use ([known answerer pins](#known-answerer-pins-over-the-tunnel),
WO-122). Across machines the answerer's **seed** lives where the answerer runs, and the asker
needs that answerer's **public key** to pin it.

```sh
mkdir -p ~/.hivebus
head -c 32 /dev/urandom | base64 > ~/.hivebus/wl.seed     # one time, on the machine that ANSWERS
```

**Today (manual):** copy the answerer's public key to the asker side out-of-band (e.g. scp the
`--public-key-file` output), or let keyless `ask` resolve + pin it from the bus on first use
and confirm the printed fingerprint by hand.

**Automated (future):** `bulwark ssh` performs this key handoff at dispatch — it places the
architect public key + a fresh per-dispatch worker seed on the remote and prints the worker's
pinnable fingerprint, so the operator pins it **before** first contact. See the bulwark remote
dispatch guide. This runbook is the manual path that handoff automates.

---

## 2. The tunnel

Start the bus on the operator, bound to loopback:

```sh
# operator: the bus (WORKLEDGER_API_KEY unset so serve does not reach workledger)
env -u WORKLEDGER_API_KEY hivebus serve --auth-disabled \
    --listen 127.0.0.1:7097 --db ~/.hivebus/bus.db --artifacts-dir ~/.hivebus/art
```

Open the reverse tunnel so the remote can reach that loopback port as its own `localhost`:

```sh
# operator -> remote: forward the remote's localhost:7097 back to the operator's bus
ssh -N -R 127.0.0.1:7097:127.0.0.1:7097 user@worker-vm
```

Now `http://127.0.0.1:7097` on the **remote** is the operator's bus. Both binds are
loopback-only; nothing listens on a public interface. (When `bulwark ssh` dispatches the
worker it opens this same `-R` forward as part of dispatch; here we open it by hand.)

> Keep the `ssh -R` session open in its own terminal for the duration. If it drops, asks fail
> with a transport error (see [Failure modes](#failure-modes)) — distinct from `no_answer`.

---

## Direction A — the remote reports back (operator asks, remote answers)

The remote worker is the warm answerer for its own checkout; the operator asks it.

> **Address the remote repo by its REMOTE absolute path.** The answerer signs the
> `repo_id` it observes — the repo's **canonical path on the remote** (e.g.
> `/home/worker/checkout-api`). The operator's `--repo` must be that same remote path,
> passed literally. Do **not** use a `~/…` or operator-local path: `ask` canonicalizes
> `--repo` against the operator's filesystem, so a local-looking path addresses the wrong
> repo and the answer is rejected with `answer observed wrong repo: <remote> != <local>`.
> The operator does not need a local clone — it only needs to name the remote path.

### fish

```fish
# --- remote (worker-vm): the warm answerer for THIS remote checkout ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent worker/remote --session-id remote-1 \
    --signing-key (cat ~/.hivebus/wl.seed) \
    --repo /home/worker/checkout-api --project checkout-api

# --- operator: ask the remote for its signed repo_status ---
# --repo is the REMOTE path the answerer signed, passed literally:
echo "what repo status did you observe?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to worker/remote --from architect/operator --type repo_status \
    --repo /home/worker/checkout-api \
    --session-id operator-asker-1 --timeout 6s
```

### bash

```bash
# --- remote (worker-vm): the warm answerer ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent worker/remote --session-id remote-1 \
    --signing-key "$(cat ~/.hivebus/wl.seed)" \
    --repo /home/worker/checkout-api --project checkout-api

# --- operator: ask (--repo = the REMOTE path, literal, not ~/…) ---
echo "what repo status did you observe?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to worker/remote --from architect/operator --type repo_status \
    --repo /home/worker/checkout-api \
    --session-id operator-asker-1 --timeout 6s
```

### sh / zsh / POSIX

```sh
# --- remote (worker-vm): use the repo's absolute path on the remote ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent worker/remote --session-id remote-1 \
    --signing-key "$(cat "$HOME/.hivebus/wl.seed")" \
    --repo /home/worker/checkout-api --project checkout-api

# --- operator: --repo is that same REMOTE absolute path, literal ---
echo "what repo status did you observe?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to worker/remote --from architect/operator --type repo_status \
    --repo /home/worker/checkout-api \
    --session-id operator-asker-1 --timeout 6s
```

The operator passes `--repo` to name the addressed repository — the remote's own absolute
path (see the callout above). Because the operator has no local clone of that checkout,
verification lands at `binding_level: repo_id_only` — the honest remote tier. `--type
repo_status` is the only resolver class today.

---

## Direction B — the remote asks the operator (operator answers, remote asks)

Mirror image: the operator runs the warm answerer; the remote consults it over the tunnel.
The same path rule applies, mirrored: the remote addresses `--repo` by the **operator's**
absolute path (what the operator answerer signs), passed literally — not a `~/…` that would
expand against the remote's own `$HOME`.

### bash (fish/sh differ only in `(cat …)` vs `"$(cat …)"`, as above)

```bash
# --- operator: the warm answerer (its own repo, operator-local path) ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent architect/operator --session-id operator-1 \
    --signing-key "$(cat ~/.hivebus/wl.seed)" \
    --repo /Users/operator/checkout-api --project checkout-api

# --- remote (worker-vm): --repo is the OPERATOR's absolute path, literal ---
echo "what repo status did you observe?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to architect/operator --from worker/remote --type repo_status \
    --repo /Users/operator/checkout-api \
    --session-id remote-asker-1 --timeout 6s
```

A free-form architect **consult** lane (asking the operator's architect a natural-language
question rather than a typed `repo_status`) is a separate, not-yet-built capability (WO-126).
Until it lands, Direction B uses the existing typed resolvers only — this runbook does not
document a consult command that does not exist.

---

## Known answerer pins over the tunnel

Pinning works exactly as in the local loop: when `ask` has no `--answer-public-key`/
`--answer-public-key-file`, it resolves the addressed answerer's declared key from the bus and
pins it in `~/.hivebus/known_answerers` (mode `0600`) **on the asking machine**:

```text
agent_id base64-ed25519-public-key first-seen-rfc3339
```

On first use `ask` prints `pinned agent_id=… fingerprint=<sha256-hex>` (SHA-256 over the raw
32-byte ed25519 public key, lowercase hex). On later asks the bus-offered key must match the
pin; a mismatch is rejected as possible impersonation and the pin file is not changed.

Over a tunnel the trustworthy first introduction is the weak point. When `bulwark ssh` does the
dispatch handoff, **pin the fingerprint it printed at dispatch** before the first ask — that is
the out-of-band moment the SSH bootstrap provides. The manual fallback is to scp the public key
and pin it explicitly with `--answer-public-key-file`.

---

## Failure modes

| Symptom | Cause | Reading |
|---|---|---|
| `Error: register live ask session: Post "http://127.0.0.1:7097/...": dial tcp ... connect: connection refused` — **non-zero exit** | the `ssh -R` tunnel is down (or never opened) | **delivery failure**, distinct from `no_answer`; reopen the tunnel |
| `delivered: true, answers: 0, response_status: no_answer` — exit 0 | the addressed agent is registered but nobody answered in time | channel success; start/restart that agent's `answer` loop |
| `Error: answer public key for agent X is not available from bus session record` — non-zero exit | the addressed agent has **no live session** on the bus (never registered, or its lease expired) | not the same as `no_answer`; ensure the answerer is running and its heartbeat is current |
| `delivered: true, answers: 0, response_status: unverified` (+ `verification_error`) | an answer arrived but failed signature / route / freshness / observation-context | rejected on purpose; check the answerer's stable `--signing-key` and that `--repo` names the right repo |
| `answer key mismatch for agent … possible impersonation` | the bus offered a key different from the pin | verify identity; if rotation was intentional, remove that agent's line from `~/.hivebus/known_answerers` and ask again |
| `repo is required for repo_status ask verification` | `--repo` omitted on a `repo_status` ask | pass `--repo <addressed-repo-path>` so the asker knows what it addressed |

---

## The boundary (why it behaves this way)

- **SSH is the transport; the signature is the authenticity.** hivebus adds no TLS or auth of
  its own on this path — it would duplicate what SSH already provides on a channel the dispatch
  already opens. Confidentiality and the wire are SSH's job; message authenticity is the
  ed25519 signature's job.
- **Loopback-only, both ends.** serve binds `127.0.0.1`; the `-R` forward binds `127.0.0.1` on
  the remote. Nothing listens on a public interface; the tunnel is the only path in.
- **The binding level is named, never inflated.** Cross-machine, the asker cannot stat the
  remote `.git` inode, so it reports `repo_id_only` — the honest remote tier — not a same-host
  guarantee it cannot make.
- **A signed answer is attributable, not automatically true.** The asker verifies signature +
  correlation + route + freshness + observation-context before trusting it. A delivered query
  with no answer is a success for the channel.

---

## Verified transcript

Real two-machine run over an `ssh -R` reverse tunnel. Operator = macOS (arm64); remote =
a Linux VM (Debian 13, `aarch64`, kernel 6.12) running the worker under a fresh git repo at
`/home/<worker>/demo-repo` (HEAD `282d35c`). serve bound `127.0.0.1:7197` on the operator;
`ssh -R 127.0.0.1:7197:127.0.0.1:7197` made it the remote's `localhost`. Both directions
verified, both landing at the honest remote tier:

```text
# Direction A — operator (mac) asks, remote (VM) answers:
#   --repo /home/<worker>/demo-repo   (the REMOTE absolute path the answerer signed)
delivered: true   answers: 1   response_status: answered   binding_level: repo_id_only
  signed repo_id: /home/<worker>/demo-repo

# Direction B — remote (VM) asks, operator (mac) answers:
delivered: true   answers: 1   response_status: answered   binding_level: repo_id_only
```

Both verify at `repo_id_only` because neither end can stat the other's `.git` inode across
the network — the cross-machine tier, named not inflated. The first attempt in Direction A
addressed the repo by an operator-local path and was correctly rejected
(`answer observed wrong repo: /home/<worker>/demo-repo != /Users/<operator>/demo-repo`),
which is why the runbook addresses the repo by its **remote** absolute path. A tunnel taken
down produces a transport error and a non-zero exit
(`register live ask session: … dial tcp …: connect: connection refused`), distinct from
`no_answer`.
