# Runbook: local ask/answer dogfood loop

Run the hivebus ask/answer nervous system on one machine: a bus (`serve`), one or
more warm answerers (`answer`), and an asker (`ask`). The asker queries a warm
agent for a signed `repo_status` answer instead of crawling the filesystem.

This runbook covers **fish**, **bash**, and **sh/zsh**. The only real difference
is how each shell sets variables and captures command output.

## What success looks like

```
delivered: true   answers: 1   response_status: answered
```
with a signed answer whose `head` matches the target repo's real `git rev-parse --short HEAD`.

`delivered: true / answers: 0 / response_status: no_answer` is **also a success** for
the channel — it means the query was delivered but no agent answered (hivebus is the
ether, not the mind). It is NOT an error.

---

## 0. Build (all shells)

```sh
cd ~/dev/obstalabs-github/hivebus
make build           # produces bin/hivebus
# optional: install on PATH so `hivebus` works anywhere
sudo cp bin/hivebus /usr/local/bin/
```

The steps below assume `hivebus` is on PATH. If you skipped the install, use
`./bin/hivebus` instead of `hivebus`.

---

## 1. A stable signing key (all shells)

The answerer signs every answer. Use a **stable** 32-byte base64 seed so its public
key does not change across restarts — generate it once and save it.

```sh
mkdir -p ~/.hivebus
head -c 32 /dev/urandom | base64 > ~/.hivebus/wl.seed     # one time only
```

Derive the matching public key (the asker needs this to verify answers):

```sh
hivebus answer --print-public-key --signing-key "$(cat ~/.hivebus/wl.seed)" > ~/.hivebus/wl.pub
cat ~/.hivebus/wl.pub
```

`--print-public-key` prints the key and exits without starting the loop.

---

## 2. fish

```fish
# --- terminal 1: the bus ---
# WORKLEDGER_API_KEY is unset for serve so it does not try to reach workledger.
env -u WORKLEDGER_API_KEY hivebus serve --auth-disabled \
    --listen 127.0.0.1:7097 --db ~/.hivebus/bus.db --artifacts-dir ~/.hivebus/art

# --- terminal 2: the warm answerer (repo_status for THIS repo) ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent workledger/agent --session-id wl-1 \
    --signing-key (cat ~/.hivebus/wl.seed) \
    --repo ~/dev/obstalabs-github/hivebus --project hivebus

# --- terminal 3: ask ---
set PUB (cat ~/.hivebus/wl.pub)
echo "which checkout is canonical and what is HEAD?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to workledger/agent --from architect/session --type repo_status \
    --session-id asker-1 --answer-public-key $PUB --timeout 6s
```

Fish notes: use `set VAR value`, not `VAR=value`; use `(cmd)` for substitution,
not `$(cmd)`.

---

## 3. bash

```bash
# --- terminal 1: the bus ---
env -u WORKLEDGER_API_KEY hivebus serve --auth-disabled \
    --listen 127.0.0.1:7097 --db ~/.hivebus/bus.db --artifacts-dir ~/.hivebus/art

# --- terminal 2: the warm answerer ---
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent workledger/agent --session-id wl-1 \
    --signing-key "$(cat ~/.hivebus/wl.seed)" \
    --repo ~/dev/obstalabs-github/hivebus --project hivebus

# --- terminal 3: ask ---
PUB="$(cat ~/.hivebus/wl.pub)"
echo "which checkout is canonical and what is HEAD?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to workledger/agent --from architect/session --type repo_status \
    --session-id asker-1 --answer-public-key "$PUB" --timeout 6s
```

---

## 4. sh / zsh / POSIX

Identical to bash (the `VAR=$(...)` and `$(...)` forms are POSIX):

```sh
# terminal 1
env -u WORKLEDGER_API_KEY hivebus serve --auth-disabled \
    --listen 127.0.0.1:7097 --db "$HOME/.hivebus/bus.db" --artifacts-dir "$HOME/.hivebus/art"

# terminal 2
env -u WORKLEDGER_API_KEY hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent workledger/agent --session-id wl-1 \
    --signing-key "$(cat "$HOME/.hivebus/wl.seed")" \
    --repo "$HOME/dev/obstalabs-github/hivebus" --project hivebus

# terminal 3
PUB="$(cat "$HOME/.hivebus/wl.pub")"
echo "which checkout is canonical and what is HEAD?" | hivebus ask \
    --server http://127.0.0.1:7097 --insecure \
    --to workledger/agent --from architect/session --type repo_status \
    --session-id asker-1 --answer-public-key "$PUB" --timeout 6s
```

---

## Adding more repos

Run one `answer` per repo you want queryable. Give each a distinct `--agent`,
`--session-id`, and `--repo`, and its own seed/pubkey pair:

```sh
# a second answerer for the workledger repo
hivebus answer --server http://127.0.0.1:7097 --insecure \
    --agent workledger/wl-repo --session-id wl-repo-1 \
    --signing-key "$(cat ~/.hivebus/wl-repo.seed)" \
    --repo ~/dev/obstalabs-github/workledger --project workledger
```

Then `ask --to workledger/wl-repo --answer-public-key "$(cat ~/.hivebus/wl-repo.pub)"`.

---

## Resolvers (what you can ask)

`--type` selects the query class the answerer resolves. Today the answerer is a
**conservative resolver**: it answers only known classes and returns
`unsupported_query_class` for anything else (it never free-form guesses).

- `repo_status` — canonical_repo_path, remote, branch, head, dirty, observed_at,
  expires_at, signed, `trust_class: tool_asserted` (git read from disk at answer time).

Other classes (`file_provenance`, etc.) are separate WOs and may not be built yet.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `WORKLEDGER_API_KEY requires WORKLEDGER_URL or WORKLEDGER_HOST` | serve sees the workledger key | prefix with `env -u WORKLEDGER_API_KEY` (already in the commands above) |
| `operator-token is required for live ask send` | missing `--insecure` against an auth-disabled server | add `--insecure` |
| `agent session not found` (inbox) | the asker session was never registered | `--insecure` self-registers the asker; ensure `--session-id` is set |
| `delivered: true, answers: 0, response_status: unverified` | an answer arrived but failed signature/route/freshness | check `--answer-public-key` matches the answerer's `--signing-key`; the answer is rejected on purpose |
| `no live answerer` / `answers: 0, no_answer` | nobody is answering | start the `answer` loop for that `--to` agent first; this is not a failure |
| `unsupported delivery_mode` (manual curl) | wrong value | use `queued_delivery`, not `queued` |

---

## The boundary (why it behaves this way)

- hivebus delivers; it does not decide, wait, or judge. A delivered query with no
  answer is a success for the channel.
- A signed answer is **attributable, not automatically true**. The asker verifies
  signature + correlation (reply_to/thread) + route + freshness before trusting it.
- The answerer reads facts from disk at answer time and signs them; stale or unsigned
  answers are rejected.
