# Backup & Recovery Runbook — Lemici Platform

**Version:** 1.0  
**Last Updated:** 2026-05-11  
**Ownership:** Platform Engineering / SRE  
**Audience:** On-call engineers, DBAs, platform operators

---

## Table of Contents

1. [Overview](#1-overview)
2. [Backup Inventory & Schedule](#2-backup-inventory--schedule)
3. [Recovery Procedures by Component](#3-recovery-procedures-by-component)
4. [Disaster Recovery (Multi-Region Failover)](#4-disaster-recovery-multi-region-failover)
5. [Verification & Testing](#5-verification--testing)
6. [Contacts & Escalation](#6-contacts--escalation)

---

## 1. Overview

This document describes backup strategies and recovery procedures for all critical Lemici Platform components: PostgreSQL, Redis, Elasticsearch, Keycloak, worker templates, and application configuration.

**Assumptions:**

- Infrastructure deployed on AWS (or GCP equivalent)
- Access to cloud provider console & CLI tools
- Credentials stored in vault (AWS Secrets Manager / GCP Secret Manager)
- Read‑only backup storage bucket in a different region than production

---

## 2. Backup Inventory & Schedule

### 2.1 PostgreSQL (`franchises` database)

| Item              | Detail                                                                  |
| ----------------- | ----------------------------------------------------------------------- |
| **Backup method** | `pg_dump` logical backups + WAL archiving for PITR                      |
| **Retention**     | 30 days                                                                 |
| **Storage**       | `s3://lemici-backups/prod/postgresql/` (versioned, Object Lock enabled) |
| **Encryption**    | SSE-KMS (AWS KMS key `arn:aws:kms:…:key/lemici-db-backup`)              |
| **Schedule**      | Daily full at 02:00 UTC; WAL segments shipped every 5 min               |
| **Verification**  | Weekly restore to staging; checksum validation on upload                |

**Backup commands (executed by backup service account):**

```bash
# Full dump (daily)
pg_dump -h postgres-prod.xxxx.us-east-1.rds.amazonaws.com \
  -U lemici_backup -d franchises \
  --format=directory --compress=9 \
  --file=/tmp/backup_$(date +%Y%m%d).dump

# Upload to S3 with checksum
aws s3 cp /tmp/backup_$(date +%Y%m%d).dump \
  s3://lemici-backups/prod/postgresql/full/ \
  --storage-class STANDARD_IA

# WAL archiving (handled by RDS/CloudSQL native)
# - For RDS: automated backups + point-in-time recovery enabled
# - For self‑hosted: archive_command → wal-g / barman
```

**Recovery tools:**

- AWS RDS: Built-in point-in-time restore to new DB instance
- Self-hosted: `pg_restore` from latest dump + WAL replay

### 2.2 Redis (Session Store)

| Item              | Detail                                                                            |
| ----------------- | --------------------------------------------------------------------------------- |
| **Backup method** | RDB snapshot (`SAVE` / `BGSAVE`)                                                  |
| **Retention**     | 7 daily snapshots                                                                 |
| **Storage**       | `/var/lib/redis/dump.rdb` → copied to `s3://lemici-backups/prod/redis/`           |
| **Encryption**    | SSE-KMS                                                                           |
| **Schedule**      | Hourly snapshots; daily copy to S3 at 03:00 UTC                                   |
| **Notes**         | Sessions are ephemeral → acceptable RPO = 1 hour; **not** used as source of truth |

**Recovery:** Replace `dump.rdb` on new Redis cluster and restart. Active sessions lost; users re-login.

### 2.3 Elasticsearch (Search Indices)

| Item              | Detail                                                           |
| ----------------- | ---------------------------------------------------------------- |
| **Backup method** | Snapshot repository (S3 or GCS) using Elasticsearch Snapshot API |
| **Retention**     | 30 daily snapshots                                               |
| **Indices**       | `franchises_all` (and future per‑tenant indices)                 |
| **Schedule**      | Daily at 04:00 UTC via curator / snapshot lifecycle policy       |
| **Verification**  | Monthly restore to staging cluster                               |

**Snapshot lifecycle policy (ILM):**

```json
{
  "policy": {
    "phases": {
      "hot": { "actions": { "rollover": { "max_size": "50gb" } } },
      "warm": {
        "min_age": "1d",
        "actions": { "allocate": { "number_of_replicas": 1 } }
      },
      "delete": { "min_age": "30d", "actions": { "delete": {} } }
    }
  }
}
```

### 2.4 Keycloak Realm Configuration

| Item                                        | Detail                                                                             |
| ------------------------------------------- | ---------------------------------------------------------------------------------- |
| **Backup method**                           | `realm-export.json` + custom theme files in git                                    |
| **Retention**                               | Git history (indefinite)                                                           |
| **Storage**                                 | Git repository (private) — backup mirrors in `backup/` bucket for disaster restore |
| **Schedule**                                | Continuous — config-as-code; every change PR-reviewed                              |
| **Export command** (periodic sanity check): |                                                                                    |

| ```bash
docker exec keycloak /opt/keycloak/bin/kc.sh export \
 --dir /tmp/export --realm camunda-platform --users realm_file

````| |

### 2.5 Worker Templates & Config Rules

| Item | Detail |
|------|--------|
| **Backup method** | Git repository — all templates under `configs/templates/` |
| **Retention** | Git history |
| **Restore** | Clone repo; redeploy workers |

### 2.6 Application Configuration (`configs/config.yaml`)

| Item | Detail |
|------|--------|
| **Backup method** | Git (same repo as worker templates) |
| **Sensitive values** | Never stored in git — pulled from vault at deploy time |
| **Restore** | Rebuild from latest config + vault secrets |

### 2.7 SSL Certificates (Kong Edge)

| Item | Detail |
|------|--------|
| **Backup method** | Encrypted archive (`tar.gz`) of `/etc/kong/ssl/` + vault record of private keys |
| **Retention** | 1 year after expiration + until rotated |
| **Rotation** | Automated via Certbot / AWS ACM (90-day Let's Encrypt) |
| **Recovery** | Certbot can reissue; private key wallet backed up separately in vault |

---

## 3. Recovery Procedures by Component

### 3.1 PostgreSQL Database Loss

**Scenario:** Primary DB instance corrupted, deleted, or unavailable.

**Step-by-step:**

1. **Confirm failure** — check RDS console / `pg_isready` alerts
2. **Choose restore point** — latest automated backup or PITR within retention window
3. **Create new DB instance:**
   - AWS RDS Console → Restore from backup / point-in-time
   - Choose different subnet group if AZ failure; otherwise same AZ for speed
   - Set `skipMultiAZ=true` initially (faster) → promote to Multi-AZ after restore
4. **Update connection strings:**
   - Update Kubernetes Secret / environment variable `DATABASE_URL` in BFF deployment
   - Or update Cloud Map / service discovery if DNS name changed
5. **Run migration** (if needed): `go run cmd/migrate/main.go` (or your migration tool)
6. **Smoke test:** connect via psql; run read/write query; check worker health endpoint
7. **Cutover DNS / LB** (if old DB unreachable): already pointed at new instance
8. **Post-recovery:** verify index health, vacuum analyze, re-enable read replicas

**Expected RTO:** < 1 hour (RDS), < 2 hours (self‑hosted)
**Expected RPO:** < 5 min (WAL PITR) — no data loss or minimal

### 3.2 Redis Session Store Loss

**Scenario:** Redis cluster fails; all in‑memory sessions lost.

**Impact:** Active user sessions invalidated — users see "session expired" and must re-login. **No persistent user data stored in Redis.**

**Step-by-step:**

1. **Failover** (if Redis Cluster / ElastiCache):
   - If primary failed, promote replica → new primary
   - Update BFF config `REDIS_ADDRESS` if endpoint changed
   - Restart BFF pods to pick up new address
2. **If complete data loss** (no replica):
   - Spin up fresh Redis cluster
   - Point BFF to it; flush stale connections
3. **Validation:** Hit health endpoint; ensure `SET/GET` test key succeeds
4. **Communication:** Notify frontend team — expect spike in login traffic

**Expected RTO:** < 15 min (ElastiCache auto‑failover); < 30 min (self‑hosted rebuild)
**Data loss:** Sessions from last snapshot window (≤1 h if hourly snapshots)

### 3.3 Elasticsearch Index Loss / Corruption

**Scenario:** Index mapping corrupted or node disk failure.

**Step-by-step:**

1. **Stop writes** — scale BFF to 0 replicas or put service in maintenance mode
2. **Delete broken index:**
   ```bash
   curl -X DELETE "https://es-prod:9200/franchises_all"
````

3. **Restore from latest snapshot:**
   ```bash
   POST _snapshot/lemici_backup/snap_20260510/_restore
   {
     "indices": "franchises_all",
     "ignore_unavailable": true,
     "include_global_state": false
   }
   ```
4. **Wait for recovery** — monitor cluster health (`GET _cluster/health`) → `green`
5. **Resume service** — scale BFF back up
6. **Verify:** run a few search queries through API; check worker logs for errors

**Expected RTO:** < 2 hours  
**RPO:** Dependent on snapshot frequency (daily → potential 24h data loss of new/changed franchise records). Mitigation: increase snapshot frequency if RPO too high.

### 3.4 Keycloak Realm Corruption / Loss

**Scenario:** Realm configuration accidentally deleted or corrupted.

**Step-by-step:**

1. **Stop Keycloak updates** — disable any automated deployment pipelines for Keycloak
2. **Export working config** (if any partial state):
   ```bash
   docker exec keycloak /opt/keycloak/bin/kc.sh export \
     --dir /tmp/export --realm camunda-platform --users realm_file
   ```
   (If Keycloak container won't start, skip — proceed to git backup)
3. **Restore `realm-export.json` from git** (commit hash of last known-good):
   ```bash
   git checkout abc123 -- deployments/docker/keycloak/realm-export.json
   ```
4. **Restore theme files** from same commit:
   ```bash
   git checkout abc123 -- keycloak-theme/lemici/
   ```
5. **Re-import realm:**
   - If using Docker Compose: `docker-compose down && docker-compose up -d keycloak` (Keycloak will import on first boot if `importRealm` enabled)
   - Or use Admin REST API:
     ```bash
     curl -X POST "https://keycloak/realms" \
       -H "Authorization: Bearer $ADMIN_TOKEN" \
       -H "Content-Type: application/json" \
       -d @realm-export.json
     ```
6. **Verify login:** attempt user sign-in; check admin console loads
7. **Check clients:** ensure `lemici-frontend`, `worker-client`, `admin-cli` have correct redirect URIs and secrets
8. **Re-enroll admin 2FA** if required-action data lost — reapply `CONFIGURE_TOTP` to admin users (see KEYCLOAK-SECURITY.MD)

**Expected RTO:** < 30 min (import)  
**Data loss:** Realm configuration only — no user credentials lost (users stored in separate DB).

### 3.5 Worker Template / Config Loss

**Scenario:** Git repository corrupted or lost (extremely unlikely).

**Step-by-step:**

1. **Clone from backup mirror** (e.g., GitHub mirror, GitLab, Bitbucket):
   ```bash
   git clone git@backup-mirror:lemici/platform.git
   ```
2. **Check out latest release tag** or `main` branch
3. **Rebuild & redeploy workers**:
   ```bash
   make build && make deploy
   ```
4. **Verify:** worker jobs running; no template-not-found errors in logs

**Expected RTO:** < 15 min (if CI/CD ready)  
**RPO:** Minutes (last git push)

### 3.6 Complete Multi-Component Loss (Catastrophic)

**Scenario:** Entire AWS region unavailable (e.g., us-east-1 outage). Multiple components simultaneously failed.

**Procedure:** See **Section 4 — Disaster Recovery**. Requires multi-region failover.

---

## 4. Disaster Recovery (Multi-Region Failover)

### 4.1 Architecture

- **Primary region:** `us-east-1` (N. Virginia)
- **DR region:** `us-west-2` (Oregon) — warm standby with replicated backups only
- **DNS:** Route53 latency‑based routing with health checks; failover via weighted routing policy
- **Database:** RDS Multi-AZ + cross‑region read replica (promoted on failover)
- **Redis:** ElastiCache cluster with global datastore (if available) or rebuild from snapshot in DR region
- **Elasticsearch:** Cross‑cluster replication (CCS) or snapshot restore to DR cluster
- **Keycloak:** Stateless container — deploy fresh in DR region from latest image + realm import from backup
- **BFF & Workers:** Containerized — redeploy in DR region

### 4.2 Failover Steps

1. **Declare incident** — confirm region-wide outage (AWS Personal Health Dashboard)
2. **Promote DB replica** in DR region:
   - AWS RDS → "Promote read replica" → becomes writeable primary
   - Update connection strings in DR BFF config to point to new DB endpoint
3. **Recreate Redis cluster** in DR:
   - Restore latest snapshot from S3 backup bucket (which has cross‑region replication)
   - Update BFF `REDIS_ADDRESS`
4. **Restore Elasticsearch** in DR region from S3 snapshot
5. **Deploy Keycloak** in DR — pulled Docker image + realm import from git (realm‑export.json)
6. **Deploy BFF & workers** in DR region (Kubernetes / ECS) — env vars point to DR DB/Redis/ES
7. **Update Route53:** Change weighted routing → 100% to DR region endpoints
8. **Smoke test:** end‑to‑end login, search, franchise creation
9. **Post‑mortem:** Document timeline; update DR runbook based on gaps

**Expected RTO:** ≤ 4 hours (most critical services)  
**Expected RPO:** ≤ 1 hour for DB (RDS cross‑region replica lag ≈ minutes); longer for ES (daily snapshot)

---

## 5. Verification & Testing

### 5.1 Daily Checks

- Backup job succeeded (CloudWatch / Stackdriver alarms)
- Latest backup file exists in S3 with expected size
- RDS automated backup status = "SUCCESSFUL"
- Redis RDB snapshot present in backup bucket

### 5.2 Weekly Restores

| Component     | Test Description                                                                     | Owner      |
| ------------- | ------------------------------------------------------------------------------------ | ---------- |
| PostgreSQL    | Restore latest full dump to staging DB; run schema migrations; verify row counts     | DBA        |
| Redis         | Restore latest RDB to test cluster; verify key TTLs; connect BFF to test cluster     | Platform   |
| Elasticsearch | Restore snapshot to staging cluster; run search queries; reindex if mapping mismatch | Search Eng |
| Keycloak      | Import realm-export into test realm; verify clients, users, events                   | Auth Eng   |
| Templates     | Deploy latest templates from git to test worker cluster; execute sample job          | QA         |

### 5.3 Quarterly DR Drill

- **Scope:** Simulate region failure → execute Section 4 steps in non‑prod environment
- **Goal:** Verify RTO ≤ 4 h; discover dependencies & missing IAM permissions
- **Post‑drill:** Update runbook; automate any manual steps identified

---

## 6. Contacts & Escalation

| Role                   | Name | Contact                | PagerDuty         |
| ---------------------- | ---- | ---------------------- | ----------------- |
| Platform SRE           | TBD  | sre@lemici.com         | `lemici-platform` |
| Database Admin         | TBD  | dba@lemici.com         | `lemici-db`       |
| Security Engineer      | TBD  | security@lemici.com    | `lemici-security` |
| Engineering Manager    | TBD  | eng-manager@lemici.com | —                 |
| CTO (final escalation) | TBD  | cto@lemici.com         | —                 |

**Emergency bridge:** Create Slack channel `#incident-<date>`; invite all responders.

---

## Appendix A — Backup Job References

| Job / Cron             | Location                                                | Description                                       |
| ---------------------- | ------------------------------------------------------- | ------------------------------------------------- |
| `pg_dump` daily        | AWS EventBridge Schedule expression `cron(0 2 * * ? *)` | Triggers Lambda → EC2 runbook doc                 |
| Redis snapshot copier  | Kubernetes CronJob `redis-backup-copy` (3× daily)       | Copies RDB from pod to S3                         |
| Elasticsearch curator  | `curator-cron` (daily 04:00 UTC)                        | Deletes snapshots >30d; creates daily snapshot    |
| Keycloak config backup | Git webhook / CI pipeline step                          | On merge to main, validates realm-export compiles |
| Worker templates       | Git push trigger                                        | CI lints templates; tags release                  |

---

## Appendix B — Recovery Command Cheat Sheet

```bash
# PostgreSQL restore from S3 (RDS)
aws rds restore-db-instance-from-s3 \
  --db-instance-identifier lemici-dr-20260511 \
  --s3-bucket-name lemici-backups \
  --s3-prefix prod/postgresql/full/backup_20260510.dump \
  --engine postgres \
  --db-instance-class db.r6g.large \
  --master-username lemici_backup
# NOTE: Use RDS console for point-in-time restore (simpler)

# Redis restore (ElastiCache)
aws elasticache create-cache-cluster \
  --cache-cluster-id lemici-dr-redis \
  --cache-node-type cache.r6g.large \
  --engine redis \
  --snapshot-arn arn:aws:elasticache:us-west-2:123456:snapshot:lemici-20260510

# Elasticsearch restore
curl -X POST "https://es-dr.us-west-2.es.amazonaws.com/_snapshot/lemici_backup/snap_20260510/_restore" \
  -H "Content-Type: application/json" \
  -d '{"indices": "franchises_all"}'

# Keycloak realm import (container)
docker cp realm-export.json keycloak-dr:/tmp/
docker exec keycloak-dr /opt/keycloak/bin/kc.sh import \
  --dir /tmp --realm camunda-platform --override true
```

---

**End of runbook.** Keep this document up‑to‑date as architecture evolves. Perform quarterly DR drills to validate procedures.
