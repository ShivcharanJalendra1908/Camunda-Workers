# Photo Upload BPMN — Trade-Off Analysis

## Decision Point

CTO insists on a separate `photo-upload.bpmn`. Current implementation uses
`user-profile-update.bpmn` with `action=update_personal` for all photo uploads.
This document analyzes the trade-offs and impact.

---

## Current Architecture (Option C — Recommended)

```
Handler uploads to S3
  → Injects key into profileData["profile_image"]
  → Starts "user-profile-update" with action=update_personal
  → Task_UpdatePersonalDetails writes profile_image + name/phone in one UPDATE
  → Worker returns BaseOutput{OldProfileImage: oldKey}
  → Handler calls launchOldPhotoCleanup() to delete old S3 object
  → Handler calls constructPhotoURLs() to build CDN URL
  → Returns 200 with CDN URL
```

**All 9 phases of photo storage migration are complete and committed.**

---

## Atomicity Analysis

### The Core Problem

S3 upload and DB write are **not atomic**. If one succeeds and the other fails,
data consistency is broken.

### Scenario 1: Without `photo-upload.bpmn` (Current)

```
Handler uploads to S3              ← S3 object exists
Handler starts user-profile-update
  → Task_UpdatePersonalDetails fails
  → Task_HandleError sends error response
Handler receives error response
Handler returns error to client
                                   ← NEW S3 object ORPHANED (never deleted)
```

`launchOldPhotoCleanup` only runs on **success** (line 474). On failure/timeout,
the newly uploaded S3 object is never cleaned up.

### Scenario 2: With `photo-upload.bpmn` (Separate Workflows)

```
Handler uploads to S3
Handler starts photo-upload BPMN
  → writes s3Key to users.profile_image   ← Photo persisted
  → returns success
Handler starts user-profile-update BPMN
  → Task_UpdatePersonalDetails fails
  → returns error
                                   ← Photo in DB but name/phone failed
                                   ← User sees error, photo already written
```

### Scenario 3: With `photo-upload.bpmn` (Audit Only — Option B)

```
Handler uploads to S3
Handler starts photo-upload BPMN
  → validates ownership
  → writes audit log (no DB write)
  → returns success
Handler starts user-profile-update BPMN
  → Task_UpdatePersonalDetails writes profile_image + name/phone
  → fails
                                   ← S3 object orphaned
                                   ← DB unchanged (atomic!)
```

**Key insight:** Even with Option B, S3 orphaning still occurs. Both options
rely on handler-level compensation (delete S3 object on failure).

### Race Condition: Two-Workflow Design

If `photo-upload.bpmn` writes `users.profile_image` and `user-profile-update.bpmn`
writes `name/phone/location`, these are **two separate transactions**:

1. `photo-upload` commits `profile_image` to DB
2. `user-profile-update` fails
3. `profile_image` is persisted but name/phone are not
4. User sees error — but photo is already written

This violates atomicity: the user expects either **all fields update or none**.

**Without `photo-upload.bpmn`**, this race condition doesn't exist because
`Task_UpdatePersonalDetails` writes `profile_image` + name/phone in a **single
SQL UPDATE** (profile.go:287-326).

---

## Option B: `photo-upload.bpmn` (Audit Only)

### Design

`photo-upload.bpmn` handles validation + audit only. NO DB write to `users` table.
`user-profile-update.bpmn` handles the actual DB write (atomic with other fields).

### Flow

```
Handler uploads to S3
Handler starts photo-upload BPMN
  → Task_ValidateOwnership (verifies userId owns entity)
  → Task_WriteAuditLog (INSERT_PHOTO_AUDIT with old/new keys)
  → returns success + auditId
Handler starts user-profile-update BPMN
  → Task_UpdatePersonalDetails writes profile_image + name/phone (single UPDATE)
  → returns success
Handler on failure → delete S3 object (compensation)
```

### Files Requiring Changes (7 total)

| # | File | Change | Severity |
|---|---|---|---|
| 1 | `bpmn/photo-upload.bpmn` | Remove 3 persist tasks, simplify to: ValidateOwnership → AuditLog → SendResponse | Major |
| 2 | `internal/workers/data-access/franchise-postgres/handler.go` | Add `VALIDATE_PHOTO_OWNERSHIP` + `INSERT_PHOTO_AUDIT` to switch (lines 592-611) + allowed ops list (lines 130-149) | Medium |
| 3 | `internal/workers/data-access/franchise-postgres/profile.go` | Add 2 new handler functions: `handleValidatePhotoOwnership()`, `handleInsertPhotoAudit()` | Major |
| 4 | `internal/workers/data-access/franchise-postgres/models.go` | Add `ValidatePhotoOwnershipInput` + `InsertPhotoAuditInput` structs | Minor |
| 5 | `internal/api/handlers/workflow_handler.go` | Start `photo-upload` first → wait → then start `user-profile-update` → add S3 compensation on failure | Major |
| 6 | `internal/api/handlers/workflow_handler_integration_test.go` | Add two-workflow flow tests | Minor |
| 7 | `user-profile/PHOTO_FLOW_VALIDATION.md` | Update flow documentation | Minor |

### Breakdown by Work Type

| Work | Files |
|---|---|
| BPMN redesign | `photo-upload.bpmn` |
| New worker handlers (2) | `profile.go`, `handler.go`, `models.go` |
| Handler orchestration (two-workflow wait) | `workflow_handler.go` |
| Tests + docs | `integration_test.go`, `PHOTO_FLOW_VALIDATION.md` |

### Key Risks

1. **Doubled latency** — Two workflow calls = 60s max timeout (vs 30s current)
2. **Compensation complexity** — Handler must clean up S3 on failure
3. **No true atomicity gain** — S3 orphaning still occurs, handler-level cleanup still needed

---

## Comparison: Option C vs Option B

| Metric | Option C (Current) | Option B (Audit Only) |
|---|---|---|
| Files changed | 0 (already working) | 7 |
| Workflow calls per request | 1 | 2 |
| Max latency (timeout) | 30s | 60s |
| Atomicity | Single SQL UPDATE | Same (single UPDATE) |
| S3 orphaning | Handler-level cleanup | Same (handler-level) |
| Audit granularity | profileData delta | Photo-specific old/new keys |
| Race condition risk | None (single workflow) | None (photo-upload doesn't write DB) |
| BPMN count | 1 | 2 |

---

## Conclusion

**Option C (current implementation) is recommended** because:

1. It's already working — all 9 phases complete and committed
2. Single workflow = single SQL UPDATE = true atomicity for DB writes
3. No two-workflow orchestration complexity
4. No doubled latency
5. The atomicity gap (S3 orphaning) exists in both options and is handled by
   handler-level compensation

The only benefit of Option B is photo-specific audit trail (old/new keys),
which is marginal — the current `INSERT_PROFILE_AUDIT` already records the
`profile_image` key in `profileData.changes`.

If the CTO insists on a separate BPMN, Option B is the cleanest approach
with 7 files changed and no atomicity regression.
