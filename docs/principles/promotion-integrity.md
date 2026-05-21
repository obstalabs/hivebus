# Promotion Integrity

WO-54 draws a hard line between promotion attempts and promotion truth.

- `promotion.pending` records are diagnostic only. They explain what Hivebus
  attempted and why it failed, but they are not recovery-safe truth.
- Recovery may only project envelopes that either:
  - carry `trace.promotion_status=passed`, or
  - predate the field entirely and therefore have an empty status during the
    WO-61 compatibility window.
- `FinalizePromotion` is the only path that can move promotion envelopes into
  the verified lane, and every finalized envelope must carry
  `trace.promotion_status=passed`.

This keeps the thread useful after a failed promotion without letting a failed
attempt masquerade as a verified diagnosis or a canonical Workledger handoff.
