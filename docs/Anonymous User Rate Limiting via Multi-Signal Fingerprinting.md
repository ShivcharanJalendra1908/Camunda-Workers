# Anonymous User Rate Limiting via Multi-Signal Fingerprinting

**Tech Design Document (Go + Gin + Redis)**

---

## 1. Objective

Design and implement a **production-grade anonymous usage control system** that:

- Limits non-authenticated users to **3 free API requests**
- Prevents trivial bypass via:
  - incognito/private browsing
  - browser switching
  - same-network (WiFi) abuse

- Operates without mandatory login

---

## 2. Design Principles

1. **Probabilistic Identity, Not Absolute Identity**
   Anonymous users cannot be uniquely identified → use correlation of signals.

2. **Multi-Signal Identity Graph**
   - Browser fingerprint
   - Cookie-based identifier
   - IP + subnet heuristics
   - Behavioral patterns

3. **Progressive Enforcement**
   - Do not rely on hard blocking alone
   - Escalate from allow → limit → CAPTCHA → login

4. **Server-Side Authority**
   - All enforcement decisions must happen on backend
   - Frontend signals are advisory only

---

## 3. High-Level Architecture

### Components

- Frontend (React or equivalent)
- Fingerprinting library (recommended: FingerprintJS)
- Backend API (Go + Gin)
- Redis (state + identity graph)
- Optional: CAPTCHA provider

---

## 4. Identity Model

### Identity Signals

| Signal              | Type        | Reliability |
| ------------------- | ----------- | ----------- |
| Cookie (visitor_id) | Persistent  | High        |
| Fingerprint         | Semi-stable | Medium      |
| IP Address          | Weak        | Low         |
| Subnet (/24)        | Contextual  | Medium      |

---

### Identity Resolution Priority

```text
1. Cookie (visitor_id)
2. Fingerprint match
3. IP heuristic
4. New identity creation
```

---

## 5. Request Lifecycle

### Step 0 — Frontend Initialization

- Load fingerprinting library
- Generate `fingerprint_hash`
- Retrieve `visitor_id` from:
  - Cookie (primary)
  - localStorage (fallback)

---

### Step 1 — Identity Initialization API

**Endpoint:**

```
POST /identity/init
```

**Headers:**

```
X-Fingerprint: <fingerprint_hash>
```

---

### Step 2 — Backend Identity Resolution

#### Logic

```go
func ResolveVisitor(fingerprint, ip string) string {
    // 1. fingerprint lookup
    vid, err := rdb.Get(ctx, "fp:"+fingerprint).Result()
    if err == nil {
        return vid
    }

    // 2. IP heuristic reuse
    vids := rdb.SMembers(ctx, "ip:"+ip).Val()
    if len(vids) == 1 {
        return vids[0]
    }

    // 3. create new identity
    vid = uuid.NewString()

    rdb.Set(ctx, "fp:"+fingerprint, vid, 7*24*time.Hour)
    rdb.SAdd(ctx, "ip:"+ip, vid)

    return vid
}
```

---

### Step 3 — Identity Issuance

**Response:**

```
Set-Cookie: visitor_id=<id>; HttpOnly; Secure; SameSite=Lax; Max-Age=604800
```

Also return in JSON for frontend storage.

---

### Step 4 — Protected API Calls

**Headers:**

```
X-Visitor-Id: <visitor_id>
X-Fingerprint: <fingerprint_hash>
```

---

## 6. Redis Data Model

### Core Keys

```
usage:{visitor_id} -> int (TTL: 24h)

fp:{fingerprint_hash} -> visitor_id (TTL: 7d)

ip:{ip} -> set(visitor_ids) (TTL: 24h)

visitor:{id}:fps -> set(fingerprints)
visitor:{id}:ips -> set(ips)
```

---

### Optional Extensions

```
flag:visitor:{id} -> suspicious
ip_block:{subnet} -> rate limit counter
```

---

## 7. Enforcement Middleware (Gin)

```go
func EnforceLimit(rdb *redis.Client) gin.HandlerFunc {
    return func(c *gin.Context) {
        vid := c.GetHeader("X-Visitor-Id")
        fp := c.GetHeader("X-Fingerprint")
        ip := c.ClientIP()

        if vid == "" {
            c.AbortWithStatusJSON(401, gin.H{"error": "missing identity"})
            return
        }

        // fingerprint reconciliation
        existingVID, _ := rdb.Get(ctx, "fp:"+fp).Result()
        if existingVID != "" && existingVID != vid {
            vid = existingVID
        }

        key := "usage:" + vid
        count, _ := rdb.Incr(ctx, key).Result()

        if count == 1 {
            rdb.Expire(ctx, key, 24*time.Hour)
        }

        if count > 3 {
            c.AbortWithStatusJSON(429, gin.H{
                "error": "limit exceeded",
                "action": "login_required",
            })
            return
        }

        rdb.SAdd(ctx, "visitor:"+vid+":fps", fp)
        rdb.SAdd(ctx, "visitor:"+vid+":ips", ip)

        c.Next()
    }
}
```

---

## 8. Anti-Abuse Mechanisms

### 8.1 Fingerprint Drift Detection

Condition:

```
visitor_id → multiple fingerprints (> threshold)
```

Action:

```
flag:visitor:{id} = suspicious
```

---

### 8.2 IP Abuse Detection

Condition:

```
ip:{ip} → too many visitor_ids
```

Action:

- throttle
- CAPTCHA enforcement

---

### 8.3 Subnet-Level Control

Track:

```
ip_block:{/24 subnet}
```

Prevents abuse from shared WiFi networks.

---

### 8.4 Identity Merging

If same fingerprint maps to multiple visitor IDs:

- unify usage under single identity
- discard duplicate IDs

---

## 9. Enforcement Strategy

| Stage      | Condition           | Action        |
| ---------- | ------------------- | ------------- |
| Allow      | usage ≤ 3           | proceed       |
| Limit      | usage > 3           | block         |
| Suspicious | anomaly detected    | CAPTCHA       |
| Abuse      | repeated violations | require login |

---

## 10. Frontend Implementation (Reference)

```javascript
import FingerprintJS from "@fingerprintjs/fingerprintjs";

const fp = await FingerprintJS.load();
const result = await fp.get();

const fingerprint = result.visitorId;

const res = await fetch("/identity/init", {
  method: "POST",
  headers: {
    "X-Fingerprint": fingerprint,
  },
});

const data = await res.json();

localStorage.setItem("visitor_id", data.visitor_id);
```

---

## 11. Observability

Track metrics:

```
requests_per_visitor
fingerprints_per_visitor
visitors_per_ip
limit_hit_rate
suspicious_flag_rate
```

Recommended stack:

- Prometheus
- Grafana
- Structured logs with visitor_id tagging

---

## 12. Limitations

This system cannot fully prevent:

- device switching
- anti-fingerprinting browsers (Tor, Brave strict mode)
- deliberate spoofing

It is designed to:

- significantly increase friction
- prevent casual abuse
- reduce automated bypass attempts

---

## 13. Production Checklist

- [ ] Fingerprinting integrated (frontend)
- [ ] Cookie-based identity implemented
- [ ] Redis identity graph deployed
- [ ] Middleware enforcing limits
- [ ] IP/subnet throttling enabled
- [ ] CAPTCHA fallback integrated
- [ ] Metrics and logging configured

---

## 14. Conclusion

Anonymous rate limiting must be treated as an **identity correlation problem**, not a key-generation problem.

The combination of:

- fingerprinting
- cookie persistence
- network heuristics
- behavioral enforcement

provides a **robust, production-ready solution** for controlling anonymous usage without authentication.
