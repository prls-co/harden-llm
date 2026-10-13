# ADR-HLLM-034: Reference history owner and artifacts

- Status: Accepted 2026-10-12; implemented and deployed 2026-10-13.
- Contract: PLAN-HLLM-REFERENCE-DECOUPLING-002 v5.3, sections 8.2–8.4.
- Tests: TEST-420, TEST-436, TEST-426/427/442/443/451.

## 1. Storage ownership

The stateful Go wrapper owns one `history` table in its isolated HLLM database
on Agent Platform PostgreSQL, using the same pool as profiles/drafts/cache.
The baseline `llm_runs` storage is the precedent, not a migration source.
Laminar receives existing sampled diagnostic OTEL spans only; no history client,
service-key query/delete integration or Laminar owner gate is required.

Store safe original inputs and resolved settings, complete terminal result,
summary/accounting and typed error. Only reference runs are recorded, including
cache hits and validated application errors. The authenticated API-key hash is
stored internally, not returned in record DTOs. Bind all SQL and key predicates.
Numbered pages/counts/stats use one fixed key/window filter. Record request plus
result is at most 17 MiB; reject oversize without truncation.

## 2. Failure and retention

History is explicitly disabled or enabled. Invalid enabled configuration is not
disabled. A healthy empty read differs from a failed read. Preserve inference
when recording fails; never replay it. Insert once with a detached 2 s context.
Known commit is saved; definite rejection is not saved. Unknown COMMIT permits
one 1 s readback: found is saved, absent/failed remains unconfirmed because the
commit can finish later. No polling, re-insert, spool or reconciler.

Rows remain until key-scoped delete/clear. Any key holder may perform them;
Portal identity adds no permission distinction. Clear freezes its cutoff and
batches at most 100 runs, reporting partial failure. Status reports key row
count and deployment table bytes. 5 GiB triggers a retention review, not a quota.

## 3. Garage artifacts

Use `artifacts/<api_key_hash>/<run_id>/<artifact_id>`, built from authenticated
scope and validated IDs. Metadata holds media type/length/digest; downloads need
no history row. Preserve trace, parse-failure and support-bundle bytes/redaction.
Bound publication at 2 s and keep inference output on failure. Delete a run's
object prefix before its history row; missing objects count as removed and
partial cleanup retains the row. Unrecorded-run orphans require manual operator
cleanup. No metadata database, journal, lifecycle rule or background worker.
Previews leave artifacts disabled; real Garage leases prove their boundary.

## 4. Deployment and evidence

P05/CHECK-422 verify shared database/role/network and Garage prefix permissions.
The existing production apply path explicitly runs the accepted gateway image's
schema command before application startup, retaining `--no-deps`. These owner
checks and TEST-450/433 are required; configuration source is not live evidence.
TEST-420/436 prove contract consistency only. Subsequent real storage, composed
and deployed checks prove their own boundaries without browsers/paid inference.

## 5. Storage representation and physical owner (2026-10-13)

P03 changes opaque `history.request` and `history.result` from the plan's JSONB
to PostgreSQL `json`; the queryable summary stays JSONB. This avoids full-body
normalization and preserves the validated payload encoding. The canonical
OpenAPI schema is loaded once and validates one exact-number tree, rather than
maintaining handwritten duplicate validators or scanning the full record again.
Use PostgreSQL microsecond precision consistently in timestamps and summaries.
Pre-resolution application failures have `selectedTarget:null`, null output and
local correlation IDs; successful results still require a complete target.
The core detaches diagnostic timestamp pointers and wrapper inputs before use.
TEST-442/451 and the P03 mutation/race regressions retain full payload equality,
the 17 MiB rejection boundary, two-second operation bounds and no replay.

The planned standalone Agent Platform server was not deployed on the host.
The approved live target is Agent Platform's existing Temporal PostgreSQL,
physically owned by `self-imp-bin-eval`: `compose-postgres-1` on `compose_default`.
Kirill approved that isolated HLLM database/role on 2026-10-13. CHECK-422 permission
and Garage lifecycle probes passed without restarting either shared owner.
This refines the physical target and does not add a PostgreSQL daemon or change
history ownership. Configuration and proof are in the
[cutover runbook](../reference-production-cutover.md) and
[P06 evidence](../../plans/evidence/harden-llm/reference-decoupling/phase-p06.md).
Schema version 1 was created fresh under the approved reset; no legacy converter
or rollback of discarded data exists.
