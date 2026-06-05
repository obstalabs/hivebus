# Hivebus Ask/Answer

`hivebus ask` is the first read-only agent-to-agent question primitive. It lets one
agent ask the warm agent a direct question and receive a signed answer without
loading the target repo or re-deriving state.

## Shape

The first slice is targeted, inband, and direct:

```sh
echo "which workledger checkout is canonical and what's HEAD?" | hivebus ask --to workledger/agent --type canonical_repo
```

The command emits a signed query envelope and a signed answer envelope. The query
uses `type=query`, the answer uses `type=answer`, both reuse the signed-envelope
substrate, and the answer points back to the query with `reply_to`.

## Read-Only Contract

A query asks for information. It does not ask the receiver to perform work.

The query payload carries:

- `question`
- `question_type`
- `read_only=true`

It must not carry `requested_action` or `executable`. That line separates query
from request: query is tell-me, request is do-something and belongs under the
authority-gated request model.

## First Slice

WO-84 intentionally builds only the narrow ask-to-answer path:

- targeted agent only
- inband only
- signed query and signed answer
- answer returns to the signer through the existing thread and reply fields

Deferred follow-ups are broadcast-query, offband-query, and answer-caching.
