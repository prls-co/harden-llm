# HLLM production cutover decisions

Accepted by the user on 2026-10-04. These decisions supersede the shared-company
proposal recorded in the [historical release journal](../docs/release-certification.md).

## 1. Private data per enabled login

Use Control Plane's stable `user_id` as HLLM's data owner. Every enabled login
may enter directly without a selected company or product grant. Two sessions
for one login share data; different logins have independent data. Control Plane
owns credentials, sessions and enabled status. HLLM owns product resources and
has no duplicate identity/account records or workspace selector.

The existing API token accesses the verification/test login's dataset through
`HARDEN_LLM_STATIC_TOKEN_USER_ID`. The token has an independent deployment-managed
lifetime; revoke it by removing or rotating configuration. Human logout never
turns a rejected human reference into a token-only request.

## 2. Clean stop/reset/start cutover (2B)

Discard the previous HLLM shared-company dataset and its dedicated artifact
objects. Certify/build the candidate and prepare inputs before stopping HLLM.
Initialize unchanged schema version 11, provision profiles separately for the
administrator and verification user, then resume. Other enabled users receive
unconfigured defaults. Leave Control Plane, shared Garage and other products
untouched. The previously exposed HLLM database password was already rotated in
the earlier cutover; no additional rotation is needed for this ownership change.

## 3. Browser-free certification and HTTP acceptance (3C)

Run the plan's fast, real-storage and browser-free release gates. Production
acceptance uses actual Phoenix login forms and temporary state markers to prove
private human data, token/verification equivalence and second-session persistence.
Restore state and log out owned probes. Fixtures cover disabled users and outages.
Record source/CI, library pin, component images, public acceptance and cleanup.
Browser layout and paid-provider behavior remain unchecked.

The detailed [implementation plan](../plans/login-owned-data-implementation-plan.md)
and [ADR-HLLM-030](../docs/adr/ADR-HLLM-030-control-plane-identity.md) are canonical.
