# AIRE spec issues to file (from Vega peering design)

> Companion to `peering-design.md` §9. These are findings surfaced by designing Vega-on-AIRE-v0.1; they are not asks specific to Vega. They are framed as implementer feedback to `github.com/aire-protocol/aire-spec`.
>
> Filing form (run from `~/Code/aire/aire-spec`):
> ```bash
> gh issue create --repo aire-protocol/aire-spec --label v0.2 \
>   --title "<title>" --body-file <(cat <<'EOF'
> <body>
> EOF
> )
> ```
> Labels: use `v0.1` for the cancellation-contract clarification, `v0.2` for the others. Skip `v0.3`/`v0.4` labels — these don't pertain to those milestones.

---

## Issue 1 — Non-normative example: layering auth on v0.1's opaque NodeID

**Title:** `v0.1: non-normative example for layering auth on opaque NodeID via a required capability`
**Label:** `v0.1`

**Body:**

§5 leaves identity at v0.1 as opaque UTF-8 NodeID, and §10 (security) is TODO. v0.2 lands DIDs and §10 will presumably specify signing. But implementations shipping today against v0.1 still need *some* way to authenticate the peer on the other end of a HELLO — otherwise the entire v0.1 implementer cohort will roll their own incompatible variants.

The cleanest fit within v0.1's existing framing is to require a custom capability whose advertisement carries authentication material. Concretely:

- Peer A advertises capability `{name: "vendor.scheme/1", required: true}` along with a payload extension carrying `HMAC(shared_secret, peer_a_node_id || nonce || timestamp)`.
- Peer B verifies the HMAC against its locally-known shared secret for `peer_a_node_id` and reciprocates.
- Either side aborts with `MISSING_REQUIRED_CAPABILITY` or `PROTOCOL_VIOLATION` on failure.

Vega is taking exactly this approach for trusted-peer federation on v0.1 (`govega/docs/peering-design.md` §3-4). The approach is explicitly a stopgap — when v0.2 ships DID-based identity, the capability is dropped wholesale; the rest of the auth stack (allowlists, grants, audit) is unaffected.

**Ask:** add a non-normative §4.5 or appendix that documents this pattern as the recommended way to layer authentication on a v0.1 connection, explicitly labeled as superseded by §5/§10 once DIDs land in v0.2. This isn't normative — implementers are free to do other things — but documenting one pattern as the "blessed" v0.1 auth recipe will prevent N variants drifting apart in the next six months.

**Out of scope:** the capability's *payload* format (HMAC, JWT, anything else) is implementation choice. Just the negotiation pattern.

---

## Issue 2 — Capability naming convention

**Title:** `§4: define capability naming convention before more implementers ship custom capabilities`
**Label:** `v0.2`

**Body:**

§4 introduces capabilities as `{name, version, required}` but doesn't constrain the `name` namespace. With multiple implementations starting to ship custom capabilities (e.g. for the auth pattern in #N), name collisions become inevitable.

Two reasonable conventions:

1. **Short namespace.** `vega.shared-secret/1`, `nous.attestation/1`. Concise but requires either a registry or first-come-first-served name allocation.
2. **Reverse-DNS.** `dev.v3ga.peering/1`, `talk.nous.attestation/1`. Self-allocating, no registry needed, longer.

Recommendation: pick one in §4 with a single-paragraph rule. For example:

> Capability names MUST be of the form `{namespace}.{shortname}/{majorversion}`, where `namespace` is a reverse-DNS prefix the implementer controls, OR a short tag registered in the AIRE capability registry (currently empty). Names beginning with `aire.` are reserved for capabilities defined in this specification.

Locking this now (v0.2 milestone) is cheap; locking it after multiple implementations ship is migration-painful.

**Out of scope:** the registry itself — that can be a later, post-v1.0 governance topic. The rule above accommodates registry-or-DNS without needing one yet.

---

## Issue 3 — Non-normative cost-accounting fields in operation final-frame payloads

**Title:** `Non-normative recommendation: cost accounting fields in operation final-frame payloads`
**Label:** `v0.2`

**Body:**

§8 BUDGET frames (v0.3) handle prospective accounting — "this op is allowed to cost up to X". But retrospective accounting — "this op actually used N tokens and cost $C" — has no protocol-level convention. Today, implementations building audit logs or chargeback systems extract this from ad-hoc application-level JSON inside STREAM payloads, with no shared shape across runtimes.

Concrete case: a runtime that proxies LLM calls across organizational boundaries (Vega's federation case, but also any AIRE-based marketplace) needs to record `tokens_in`, `tokens_out`, `cost_usd` per operation for audit. Without a recommended shape, every pair of implementations re-negotiates this in their application layer.

**Ask:** a non-normative recommendation, either as an appendix or a §3.x note:

> Implementations whose operations produce work with a meaningful unit cost (LLM tokens, compute time, API calls) SHOULD include an `accounting` object in the final STREAM (or ERROR) frame's payload with the following keys when applicable:
> - `tokens_in` (uint): input tokens consumed
> - `tokens_out` (uint): output tokens produced
> - `cost`: object `{amount: number, currency: string}` (ISO 4217)
> - `duration_ms` (uint): wall-clock duration of the operation
>
> Receivers MAY use this for audit, chargeback, or analytics. It is informational; protocol behavior MUST NOT depend on these values.

Forward-compatibility: this is the *actuals* counterpart to BUDGET's *commitments* in v0.3. Cleanly orthogonal.

---

## Issue 4 — v0.2 design: lock the handle scheme

**Title:** `v0.2 design: lock handle scheme (recommend Mastodon-style @agent@domain)`
**Label:** `v0.2`

**Body:**

`design/addressing.md` introduces handle resolution as a v0.2 feature but the canonical syntax isn't locked. Implementers building federation UX need to know which way to render handles in copy, prompt for them in forms, and parse them in routing tables.

Two candidates:

- **`@agent@domain`** (Mastodon-style). Visually distinct from URLs and DNS names; the `@`-prefix signals "this is a handle" the same way `https://` signals "this is a URL". Six years of mainstream UX validation in the fediverse.
- **`agent.domain`** (DNS-style). Familiar, but ambiguous: is `chat.example.com` a hostname or a handle? UX has to disambiguate.

Recommendation: lock `@agent@domain` in the v0.2 addressing design doc. Reasons:

1. Unambiguous on sight — a user copying a handle into a chat or settings field never confuses it with a URL.
2. Resolution rules can mirror Mastodon's WebFinger pattern (well-known endpoint) which is a familiar and battle-tested base layer.
3. Frees `agent.domain`-style DNS records to mean something else later if needed.

Once locked, the resolution flow can be normative in v0.2 §6 (URI scheme).

---

## Issue 5 — §7: explicit v0.1 cancellation contract (stream close = cancel)

**Title:** `§7: spell out the v0.1 cancellation contract (stream close = cancel)`
**Label:** `v0.1`

**Body:**

§7 is currently marked TODO for the v0.3 CANCEL frame. v0.1 implementations cancel operations *today* by closing the QUIC stream — this is correct given the v0.1 wire format, but the spec doesn't say so, and a reader could reasonably assume v0.1 has no cancellation mechanism at all.

**Ask:** one paragraph in §7, ahead of the v0.3 CANCEL-frame text:

> In v0.1, peers MAY cancel an in-flight operation by closing the corresponding QUIC stream (`STREAM_RESET` or graceful close). Receivers observing unexpected stream termination for an active OpID MUST treat it as cancellation and abort the associated work. This is the v0.1 cancellation contract; the CANCEL frame defined below (v0.3) supersedes it for cases where the connection remains open and only the specific operation is cancelled, without disturbing other operations on the same connection.

This costs ~50 words and removes a class of "does v0.1 have cancel?" questions before they arrive.

---

## Cross-issue note (do not file as an issue)

Issues 1 and 2 are coupled: if §4 doesn't constrain capability naming (Issue 2), the example in Issue 1 should use a deliberately-ugly placeholder name (e.g., `EXAMPLE.shared-secret/1`) so readers don't cargo-cult `vega.shared-secret/1` as if it were blessed. Suggest filing Issue 2 first, resolving the naming question, then revising the Issue 1 wording to use a name in the chosen convention.
