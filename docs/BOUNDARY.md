# Boundary Charter

Hivebus stays useful because it is boring at the boundary. This charter is the rule before code quality, contributor intent, or commercial pressure enter the discussion.

## The Invariant

Hivebus is a dumb signed channel. It delivers messages, verifies provenance, preserves receipts, and exposes deterministic state. It does not decide what the answer should be, generate content, judge quality, or hold presence cognition about who is warm, idle, available, or likely to know.

Anything that makes the bus smart is out of scope forever, regardless of code quality. A clever feature that teaches Hivebus to infer, rank, synthesize, or command has crossed the line even if it is well tested.

## Forever Open

The following surfaces are core commitments and stay in this repo under the open license:

- wire protocol and versioned envelope shapes
- envelope signing, signature verification, receipts, and replayable provenance
- deterministic resolvers and validation rules
- consult and escalation payload contracts, including trust-class semantics
- key distribution, key pinning, and answerer identity checks
- the answerer and asker CLI surfaces

Protocol contracts will never move behind a paid wall. Implementations may vary, but the shapes needed to speak Hivebus remain open.

## Out Of Tree

The following work does not belong in core:

- live-session bridges: hooks, proxies, or shims that make a running agent session answer from warm context
- presence and fleet cognition: availability, memory warmth, operator attention, or capability inference across live sessions
- injection into model streams or transcript flows
- directive and command channels that steer a live session instead of carrying typed requests and receipts
- vendor-session-specific integrations, adapters, or lifecycle hooks

These can live in separate integrations, including commercial integrations or third-party forks. The MIT license permits forks; core does not bless, carry, or normalize them.

## Commercial Integration

Deeper live-session integration is available commercially via [Obsta Labs](https://obstalabs.dev).
