# NeuroRouter Run Envelopes

Hivebus can preserve governed NeuroRouter execution receipts on the same typed
thread that carried intake, diagnosis, and Workledger handoff. These envelopes
are receipts, not runtime commands. NeuroRouter governs the run; Workledger owns
work truth; Hivebus preserves the thread of evidence.

## Envelope Types

The `nr.run.*` family contains nine typed lifecycle receipts:

| Type | Meaning |
| --- | --- |
| `nr.run.started` | A governed NeuroRouter bundle-run started for a Workledger WO. |
| `nr.run.context_projected` | NeuroRouter projected an approved Context Bundle for the run. |
| `nr.run.approval_pending` | A Tool Policy decision requires operator approval before the action proceeds. |
| `nr.run.tool_call` | A governed tool call was allowed or approval-gated by policy. |
| `nr.run.policy_denied` | A tool request or action was denied by Tool Policy. |
| `nr.run.completed` | The governed run reached a successful terminal state. |
| `nr.run.failed` | The governed run failed with a redacted reason. |
| `nr.run.cancelled` | The governed run was cancelled with a redacted reason. |
| `nr.run.audit_anchor` | A canonical NeuroRouter or Workledger audit anchor is available. |

## Payload Contract

Every payload is JSON and must be redacted before append. Required fields:

- `neurorouter_run_id`
- `source_thread_id`
- `occurred_at`
- `redacted`

Stable reference fields are optional unless required by a specific envelope
type:

- `workledger_wo_ref`
- `agent_bundle_ref`
- `agent_bundle_hash`
- `context_bundle_ref`
- `context_bundle_hash`
- `tool_policy_ref`
- `tool_policy_hash`
- `audit_anchor_id`
- `source_envelope_id`
- `artifact_refs`

Additional bounded metadata may include:

- `model`
- `provider`
- `cost`
- `policy`
- `redacted_output_summary`
- `tool_call_id`
- `tool_name`
- `approval_id`
- `failure_reason`

## References, Not Copies

Hivebus does not copy canonical NeuroRouter or Workledger records into the
thread. Envelopes point at stable refs and hashes.

Do not include:

- full Agent Bundle bodies
- full Context Bundle bodies
- full Tool Policy bodies
- full prompts
- model transcripts
- secrets or provider credentials
- OAuth headers
- raw tool payloads
- raw internal NeuroRouter logs

The model validator rejects unknown payload fields for `nr.run.*` envelopes.
That keeps the receipt shape tight: producers can add new references only after
the protocol names them.

## Event-Specific Requirements

| Type | Additional Requirements |
| --- | --- |
| `nr.run.started` | `workledger_wo_ref`, Agent Bundle ref or hash, Context Bundle ref or hash, and Tool Policy ref or hash. |
| `nr.run.context_projected` | Context Bundle ref or hash. |
| `nr.run.approval_pending` | `approval_id` and `policy.decision=approval_required`. |
| `nr.run.tool_call` | `tool_call_id`, `tool_name`, and `policy`. |
| `nr.run.policy_denied` | `policy.decision=denied` and `policy.reason`. |
| `nr.run.completed` | `redacted_output_summary` or `artifact_refs`; no `failure_reason`. |
| `nr.run.failed` | `failure_reason`. |
| `nr.run.cancelled` | `failure_reason`. |
| `nr.run.audit_anchor` | `audit_anchor_id`. |

## Example

```json
{
  "message_id": "msg_nr_run_001",
  "thread_id": "thr_nr_governed_run",
  "from": "service.neurorouter",
  "to": ["service.hivebus"],
  "type": "nr.run.started",
  "payload": {
    "workledger_wo_ref": "workledger://neurorouter-pro/WO-701",
    "agent_bundle_ref": "workledger://bundle/agent/preheat-investigator@sha256:aaaaaaaa",
    "context_bundle_ref": "workledger://bundle/context/nr-governance@sha256:bbbbbbbb",
    "tool_policy_ref": "workledger://policy/tool/nr-governance@sha256:cccccccc",
    "neurorouter_run_id": "nr_run_20260516_120000",
    "source_thread_id": "thr_nr_governed_run",
    "source_envelope_id": "msg_work_order_created",
    "model": "claude-sonnet",
    "provider": "anthropic",
    "policy": {
      "decision": "allowed",
      "rule_ref": "policy://tool/nr-governance/start"
    },
    "occurred_at": "2026-05-16T12:00:00Z",
    "redacted": true
  },
  "sent_at": "2026-05-16T12:00:00Z",
  "idempotency_key": "idem_msg_nr_run_001",
  "trace": {
    "correlation_id": "corr_thr_nr_governed_run",
    "span_id": "span_msg_nr_run_001",
    "model": "neurorouter",
    "verified": true
  },
  "security": {
    "scheme": "ed25519",
    "nonce": "nonce_msg_nr_run_001",
    "signed": true
  }
}
```

## Boundary

This protocol surface is Free/core and belongs in the `hivebus` repo because it
defines envelope shapes and validation. Hosted bus, RBAC, cross-team views,
approval UX, and policy administration belong in paid layers. The protocol does
not fork by tier.
