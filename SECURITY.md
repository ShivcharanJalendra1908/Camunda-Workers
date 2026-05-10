# Lemici Platform — Security Policy

**Effective Date:** 2026-05-11  
**Version:** 1.0  
**Audience:** All engineers, security team, auditors, third-party vendors  
**Scope:** Entire Lemici platform including authentication (Keycloak), API gateway, backend workers, and data stores.

---

## 1. Security Principles

1. **Defense in Depth** — multiple overlapping security controls.
2. **Least Privilege** — services and users get only the permissions they need.
3. **Zero Trust Network** — never trust internal traffic by default; all communications authenticated & authorized.
4. **Secure by Default** — safe defaults that require explicit opt-out for risky features.
5. **Auditability** — all security-relevant actions are logged with tamper-resistant audit trails.
6. **Confidentiality, Integrity, Availability (CIA)** — balanced against usability.

---

## 2. Authentication & Authorization

### 2.1 Identity Provider — Keycloak

- **Realm:** `camunda-platform`
- **Authentication method:** OIDC Authorization Code Flow with PKCE
- **Multi-factor authentication (MFA):** TOTP (Google Authenticator, Microsoft Authenticator, FreeOTP) — **optional for users, mandatory for administrators**
- **Brute force protection:** Enabled — exponential backoff, temporary lockouts, no permanent bans
- **Password policy:** Minimum 8 characters; requires uppercase, lowercase, digit, special char; excludes username
- **Session type:** Stateful BFF sessions stored in Redis; HttpOnly Secure SameSite=Lax cookies (no JWT-in-header on every request)
- **Remember-Me:** Disabled globally — no persistent browser logins
- **Direct password grant (Resource Owner):** Disabled for all public clients — only OIDC code flow allowed

**Authorization:**  
Role-based access control (RBAC) via Keycloak realm roles and client roles. Service accounts use client-credentials flow.

### 2.2 MFA Enforcement Matrix

| Role                             | MFA Required? | Enforcement Mechanism                    | Recovery Process                                          |
| -------------------------------- | ------------- | ---------------------------------------- | --------------------------------------------------------- |
| End users                        | Optional      | Self-enrollment via Account Console      | N/A (user-managed)                                        |
| Platform administrators          | **Mandatory** | `CONFIGURE_TOTP` required action applied | Secondary admin can remove required action if device lost |
| Service accounts (worker-client) | N/A           | Certificate-based or client-secret       | Rotate secrets via vault                                  |

### 2.3 Password Lifecycle

- **Change frequency:** No forced periodic rotation (NIST recommendation — only force change if compromise suspected)
- **Password reuse prevention:** Not currently enforced (backlog — implement password history)
- **Password recovery:** Email-based reset link (1–5 min validity) — rate-limited

---

## 3. Transport Security

### 3.1 TLS/SSL

- **Edge (Kong):** TLS 1.3 minimum; strong cipher suites; HTTP/2 enabled; automatic certificate rotation via ACME/Let's Encrypt or cloud provider
- **Internal service-to-service:** mTLS recommended (backlog); currently HTTP within trusted network with network policies
- **Certificate management:** Automated renewal; private keys stored in vault (AWS Secrets Manager / HashiCorp Vault)
- **HSTS:** `Strict-Transport-Security: max-age=31536000; includeSubDomains; preload` on all domains

### 3.2 CORS

- Whitelist exact origins — no wildcards for production
- Credentials allowed only for same-site and explicitly listed origins
- Headers: `Origin`, `Content-Type`, `Accept`, `Authorization`, `X-Request-ID`, `X-Session-ID`, `X-CSRF-Token`

---

## 4. Data Protection

### 4.1 Encryption at Rest

- **PostgreSQL:** TBD — enable transparent data encryption (TDE) if available (RDS/Aurora)
- **Elasticsearch:** TBD — enable encrypt‑at‑rest (AWS ES encryption, self-hosted via LUKS if needed)
- **Redis:** In‑memory; persistent storage optional — if persisted, encrypt disk (AWS ElastiCache encryption-at-rest enabled)
- **Secrets:** Never stored in repo. All secrets via environment variables sourced from secret manager (AWS Secrets Manager / Parameter Store / Vault)

### 4.2 Encryption in Transit

- All external traffic: HTTPS only
- Internal traffic (Kong → BFF → workers): mTLS recommended (backlog)
- Database connections: TLS enabled (PostgreSQL SSL mode `require` or `verify-full` for production)

### 4.3 PII Handling

- User email, name, phone number considered PII — access restricted to authorized services
- PII not logged in plain text — logs redacted via structured logging middleware
- Data retention: user data retained until account deletion; backups have same retention schedule

---

## 5. Session Management

- **Session store:** Redis (AUTH_SESSION_ID → session object with userID, expiry, CSRF token)
- **Session TTL:** 24 hours (configurable via `configs/config.yaml`)
- **Idle timeout enforced by Keycloak:** 15 minutes — BFF silent renew via iframe ensures active SPA users stay logged in
- **Session invalidation:** Logout explicitly revokes Keycloak refresh token and deletes Redis session
- **Session fixation protection:** Keycloak issues new session ID on authentication
- **Cookie flags:** `Secure; HttpOnly; SameSite=Lax` — all three enforced

---

## 6. Input Validation & Output Encoding

- **All API inputs:** validated against JSON schema, size limits, type checks (see worker security model in `internal/workers/infrastructure/template-driven/SECURITY.md`)
- **SQL/Elasticsearch queries:** parameterized only — no string interpolation
- **Templates (Freemarker):** HTML escaping enabled (`?html`) on all user-supplied values in Keycloak theme FTL files
- **Output encoding:** Context-aware — HTML, JSON, URL encoded appropriately

---

## 7. Logging, Monitoring & Alerting

### 7.1 Audit Logging

- **Keycloak events:** enabled for `LOGIN`, `FAILED_LOGIN`, `LOGOUT`, `ADMIN_EVENT`, etc. → shipped to external SIEM (Elasticsearch/CloudWatch) via log shipper (backlog)
- **API gateway (Kong):** access logs per request — retained 90 days
- **Application logs:** structured JSON with correlation IDs; severity levels; sensitive data redacted
- **Worker logs:** detailed template processing audit trail (who ran what template and when)

### 7.2 Metrics

- **Prometheus endpoint:** `/metrics` exposing counters for jobs, errors, cache hits, regex timeouts
- **Health checks:** `/health` (status) + `/ready` (dependencies up)
- **Business metrics:** (none in scope — future)

### 7.3 Alert Thresholds (Draft — Security Team Finalization Required)

| Metric                          | Threshold | Severity               | Notification |
| ------------------------------- | --------- | ---------------------- | ------------ |
| Failed logins > 50/min per IP   | Warning   | Slack #security-alerts |
| Failed logins > 5/min per user  | Medium    | Email to security@     |
| Account lockout event           | Critical  | PagerDuty + Slack      |
| API error rate > 5% (5m window) | High      | Slack #devops          |
| Worker panic / crash            | Critical  | PagerDuty              |

---

## 8. Backup & Recovery

### 8.1 Backup Scope

| Component                  | What to backup                      | Frequency                     | Retention        |
| -------------------------- | ----------------------------------- | ----------------------------- | ---------------- |
| PostgreSQL (franchises DB) | Full dump + WAL archiving           | Daily full + continuous WAL   | 30 days          |
| Redis (sessions)           | RDB snapshot                        | Every 6 hours                 | 7 days           |
| Elasticsearch indices      | Snapshot repository (S3/GS)         | Daily                         | 30 days          |
| Keycloak realm config      | `realm-export.json` + theme files   | Every config change (git)     | git history      |
| Worker templates           | `configs/templates/` directory      | Continuous (git)              | git history      |
| Worker code & configs      | Entire repo + `configs/config.yaml` | Continuous (git)              | git history      |
| SSL certificates           | Private keys + cert chain           | Before expiry + after renewal | Until superseded |

### 8.2 Backup Storage

- Backups stored in **immutable, geographically separate** object storage (AWS S3 with Object Lock or GCS with retention policy)
- Encrypted at rest using KMS (AWS KMS / GCP KMS)
- Access restricted to backup service accounts only (least privilege)

### 8.3 Recovery Procedures

See **`BACKUP-RECOVERY.md`** for step-by-step runbooks:

- Database restore (PostgreSQL point-in-time recovery)
- Elasticsearch snapshot restore
- Redis session data rebuild (non-critical — sessions can be cleared)
- Keycloak realm reimport
- Full disaster recovery (multi-region failover)

### 8.4 RPO / RTO

| Component        | Recovery Point Objective (RPO) | Recovery Time Objective (RTO) |
| ---------------- | ------------------------------ | ----------------------------- |
| PostgreSQL       | < 5 min (WAL archiving)        | < 1 hour                      |
| Redis            | 6 hours (snapshot)             | < 15 min (cluster failover)   |
| Elasticsearch    | 24 h (snapshot)                | < 2 hours                     |
| Keycloak config  | Minutes (git)                  | < 30 min (re-import)          |
| Worker templates | Minutes (git)                  | < 15 min (redeploy)           |

---

## 9. Vulnerability Management

- **Dependency scanning:** Trivy / Snyk scan container images on every PR
- **SAST:** GitHub Advanced Security or SonarQube on PRs
- **Container hardening:** Distroless / Alpine base; non-root user; read-only filesystem where possible
- **Secrets scanning:** GitGuardian / TruffleHog pre-commit hook to prevent credential leakage
- **Patch cadence:** Security patches applied within 7 days for CVSS ≥ 7.0; 30 days for lower severity
- **Penetration testing:** Annual third-party pentest; quarterly internal red-team exercise

---

## 10. Incident Response

### 10.1 Security Incident Classification

| Level    | Definition                                                           | Response Time                  |
| -------- | -------------------------------------------------------------------- | ------------------------------ |
| Critical | Active breach, data exfiltration, service compromise                 | Immediate (15 min) — PagerDuty |
| High     | Service degradation due to attack, vulnerability exploit in progress | 1 hour                         |
| Medium   | Potential security gap, suspicious activity unconfirmed              | 4 hours                        |
| Low      | Security finding with no immediate risk                              | 24 hours                       |

### 10.2 Response Playbook (High-Level)

1. **Detect** — Alert from monitoring / report from user
2. **Contain** — isolate affected system (Kong block IP, rotate credentials, disable service account)
3. **Eradicate** — remove malware/backdoor, patch vulnerability
4. **Recover** — restore from clean backup; verify integrity
5. **Post-mortem** — blameless analysis within 5 business days; update policies

### 10.3 Contacts

- **Security Team:** security@lemici.com
- **On-Call Engineer:** via PagerDuty `lemici-platform` service
- **Escalation:** CTO / VP Engineering

---

## 11. Compliance & Audits

- **SOC 2 Type II:** Audit trail from Keycloak + application logs must be retained ≥ 1 year
- **GDPR:** User data export & deletion mechanisms implemented; consent log retained
- **PCI-DSS:** If handling payment data — separate scope (currently out of scope)
- **ISO 27001:** ISMS controls mapped; annual audit

---

## 12. Exceptions & Waivers

Any temporary deviation from this policy requires:

1. Written justification from engineering manager
2. Risk acceptance signed by CISO or CTO
3. Expiration date ≤ 90 days (renewable once)
4. Documented mitigation plan

Exceptions tracked in `SECURITY-EXCEPTIONS.md` (private repo).

---

## 13. Policy Review

This policy is reviewed **annually** or after any major security incident.

**Next review:** 2027-05-11

---

## 14. Related Documents

- `KEYCLOAK-SECURITY.MD` — Detailed technical implementation of Keycloak hardening phases
- `BACKUP-RECOVERY.MD` — Step-by-step backup & disaster recovery procedures
- `internal/workers/infrastructure/template-driven/SECURITY.md` — Worker process security model
- `API-SECURITY.md` — API gateway security specifications (to be written)
- `DEPLOYMENT.md` — Secure deployment procedures

---

**Document classification:** Internal — Public non‑distribution  
**Last approval:** CTO, 2026-05-11
