# Promotion Integrity

WO-54 draws a hard line between promotion attempts and promotion truth.

- `promotion.pending` records are diagnostic only. They explain what Hivebus
  attempted and why it failed, but they are not recovery-safe truth.
- Recovery may only project envelopes that either:
  - carry `trace.promotion_status=passed`, or
  - predate the field entirely and therefore have an empty status during the
    WO-61 compatibility window, but only when the thread contains no
    authoritative verified promotion-passed pair yet. Pending, failed,
    unverified, malformed, or wrong-thread promotion envelopes do not close that
    compatibility window.
- `FinalizePromotion` is the only path that can move promotion envelopes into
  the verified lane, and every finalization must commit the complete
  authoritative pair for one thread:
  - one verified `diagnosis.proposed` envelope, and
  - one verified `work_order.create` envelope.
  Every finalized envelope must carry `trace.verified=true` and
  `trace.promotion_status=passed`, and the payloads must pass the same
  semantic checks recovery depends on: the diagnosis validates, has
  `verified=true`, has no unresolved `missing_info`, and the work-order receipt
  declares `tracking_system=workledger`, `workledger_project`, positive
  `work_order_id`, `work_order_title`, and a `source_thread_id` matching the
  thread. Non-Workledger execution receipts are not accepted by
  `FinalizePromotion`; they need their own explicit receipt contract rather
  than weakening the canonical Workledger handoff.
- Retrying the same promote request after a successful finalization is a
  duplicate no-op. Hivebus reconciles the deterministic diagnosis envelope and
  canonical Workledger receipt already in the verified lane before it writes a
  new pending record or calls Workledger again, so a lost client response cannot
  create a second work order or a spurious `promotion.failed` diagnostic.
- Operators cannot append fresh verified `diagnosis.proposed` or
  `work_order.create` envelopes with empty or `passed`
  `trace.promotion_status` through `/v0/threads/{threadID}/messages`; empty
  status is reserved for bounded legacy recovery only, and `passed` is reserved
  for `FinalizePromotion`.

This keeps the thread useful after a failed promotion without letting a failed
attempt masquerade as a verified diagnosis or a canonical Workledger handoff.
