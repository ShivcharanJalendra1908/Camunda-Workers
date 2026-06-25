# Guest Quota System — How It Works

## What Is This?

Imagine you walk into a shop. The shop lets you try 3 products for free before asking you to register. That's exactly what this system does — it lets website visitors use our AI features a limited number of times for free, then asks them to sign up.

---

## The Problem We Solved

Without this system, anyone could:
- Visit our website unlimited times
- Use our AI features (like asking questions about franchises) without creating an account
- Cost us money on server bills with no way to convert them into registered users

We needed a way to say: **"You can try this 3 times for free. After that, please sign up."**

---

## How It Works (The Simple Version)

### Step 1: Who Are You?

When someone visits our website, their browser sends two things:
- **A device token** — like a digital fingerprint stored in the browser
- **A device salt** — a secret number that makes the fingerprint unique

If they don't have these (first-time visitor), we use their IP address + browser info instead.

We combine these into a unique ID (called a "composite key") — think of it like a name tag that identifies the device without knowing the person's real name.

### Step 2: Are You a Robot or Troublemaker?

Before counting anything, we check if this visitor looks suspicious:
- **Are they making too many requests too fast?** (like a robot)
- **Are the same IP address and device appearing suspiciously often?**
- **Are they on our block list?**

If yes → we block them immediately. If no → we continue.

### Step 3: Do You Have a Valid Session?

If the visitor already has an active session (they started using AI recently), we let them continue where they left off. No new credit is used.

### Step 4: Are You Out of Credits?

We check: **How many times has this device used AI in the last 30 days?**

- **Used 0, 1, or 2 times** → Allow. Use one credit. Now they have 1 fewer credit left.
- **Used 3 times** → Block. Show a message: "You've used all 3 free sessions. Sign up for unlimited access."

### Step 5: Create a Session

If allowed, we create a new session. Each session lets the user ask up to **6 questions**. After 6 questions, the session ends and a new one starts (using another credit).

---

## Key Concepts Explained

### Credits vs Sessions

Think of it like a phone plan:
- **Credits** = Your monthly allowance (3 free tries per 30 days)
- **Sessions** = Individual call periods (each session allows 6 questions)

You use 1 credit to start a session. Within that session, you can ask 6 questions. When you're done (or after 24 hours), the session ends. Next time you come back, you start a new session (using another credit).

### The 30-Day Window

Your 30-day window starts when you **first** use the system, not on a calendar date. So if you first visit on August 15, your credits reset on September 14 — exactly 30 days later.

This is tracked automatically using Redis (a fast data store). The system sets a countdown timer when you first use AI. When the timer runs out, your credits reset.

### Why 30 Days?

It's a balance:
- Long enough that visitors don't feel rushed to sign up
- Short enough that we're not giving away unlimited free access

---

## What Happens at Each Step

Here's what happens behind the scenes when someone visits `/api/v1/ai/query`:

```
Visitor sends request
        ↓
[Signal Middleware] → Builds their identity (composite key)
        ↓
[Anomaly Middleware] → Checks for suspicious behavior
        ↓
[Quota Middleware] → Checks credits and sessions
        ↓
    ┌─────────────────────────────────┐
    │  Has valid session?             │
    │  YES → Continue session (free)  │
    │  NO  → Need new credit          │
    │      └─ Out of credits?         │
    │         YES → Show "Sign Up"    │
    │         NO  → Use 1 credit,     │
    │              start new session  │
    └─────────────────────────────────┘
        ↓
    Request reaches AI handler
```

---

## What The Visitor Sees

### Successful Request
The response includes headers telling the frontend:
- `X-Session-Credits-Used: 1` — How many credits used so far
- `X-Session-Credits-Remaining: 2` — How many credits left
- `X-Session-Queries-Used: 3` — Questions asked in this session
- `X-Session-Queries-Remaining: 3` — Questions left in this session
- `X-Quota-Status: ok` — Overall status (ok / last_credit / exhausted)

### Out of Credits
```json
{
  "code": "SIGNUP_REQUIRED",
  "reason": "monthly_credits_exhausted",
  "creditsUsed": 3,
  "creditsLimit": 3,
  "resetIn": 2592000,
  "message": "You've used all 3 free sessions. Sign up for unlimited access.",
  "signupUrl": "/register"
}
```

The `resetIn` tells the frontend exactly how many seconds until credits reset (the actual Redis TTL, not a guessed date).

---

## The Three Layers of Protection

### Layer 1: Device Identity (Signal Middleware)
- Builds a unique ID from device token + salt
- Falls back to IP + browser info if no token
- Rejects requests without a salt (required for tracking)

### Layer 2: Abuse Detection (Anomaly Middleware)
- **S1:** Flags if too many different device tokens come from the same IP (catches shared proxies)
- **S2:** Flags if one device token appears from too many IPs (catches stolen tokens)
- **S3:** Flags burst activity (3+ requests in 60 seconds)
- **S4:** Warns when device token is missing (uses fallback identity)
- **S5:** Checks a block list (for confirmed abusers)

All checks are **fail-open** — if something breaks, we let the user through rather than blocking everyone.

### Layer 3: Credit & Session Enforcement (Quota Middleware)
- Tracks credits per device per 30-day window
- Tracks questions per session
- Enforces limits and returns appropriate error codes

---

## Where Things Are Stored

### Redis (Fast In-Memory Store)
| What | Key Format | Expires After |
|------|-----------|---------------|
| Credit count | `quota:ai:{device-id}` | 30 days (auto-reset) |
| Active session ID | `guest:active_session:ai:{device-id}` | 24 hours |
| Questions asked | `session:{session-id}:queries` | 24 hours |
| Block status | `block:guest:{device-id}` | 24 hours |

### PostgreSQL (Permanent Database)
| What | Table |
|------|-------|
| Audit log | `guest_audit_log` (who did what, when, any anomaly flags) |

---

## For Developers: Adding a New Protected Route

Say you want to protect `/api/v1/feedback` with a limit of 20 submissions per 7 days.

### Step 1: Add Config
```yaml
# In configs/config.yaml under guest.route_groups:
    feedback:
      credits_per_window: 20
      credit_window_days: 7
      queries_per_session: 1
      session_ttl_seconds: 86400
      credit_key_ttl: "168h"
      session_queries_ttl: "24h"
      active_session_ttl: "24h"
```

### Step 2: Wire the Route
```go
// In cmd/api-gateway/main.go, inside the if cfg.Guest.Enabled block:
publicAPI.POST("/feedback",
    middleware.GuestSignalMiddleware(cfg),
    middleware.GuestAnomalyMiddleware(redisClient.GetClient(), cfg, guestAuditRepo),
    middleware.GuestQuotaMiddleware(redisClient.GetClient(), cfg, "feedback"),
    handler.SubmitFeedback,
)
```

That's it. No other code changes needed.

---

## Frequently Asked Questions

**Q: What if Redis goes down?**
A: The system "fails open" — it lets all requests through rather than blocking everyone. We'd rather give away some free AI than lose all visitors.

**Q: What if someone deletes their cookies?**
A: The device token is stored in the browser. If they clear cookies, they get a new identity and fresh credits. That's why we also check IP + browser info as a fallback.

**Q: What about logged-in users?**
A: They skip the entire quota system. The middleware checks for a valid session cookie first and lets them through immediately.

**Q: Can someone abuse this by using multiple browsers?**
A: Each browser gets its own device token, so yes — they'd get separate credit pools. The anomaly detection (S1, S2) catches extreme abuse patterns, but normal users sharing a laptop are fine.

**Q: How does the frontend know when to show "Sign Up"?**
A: It reads the `X-Quota-Status` header: `ok`, `last_credit`, or `exhausted`. When `exhausted`, show the signup prompt. The `X-Session-Credits-Remaining` header tells them exactly how many tries they have left.

---

## Summary

| Feature | How It Works |
|---------|-------------|
| Free limit | 3 credits per device per 30 days |
| Session limit | 6 questions per session |
| Tracking | Device token + salt (or IP fallback) |
| Reset | 30 days from first use (rolling window) |
| Abuse protection | 5-layer anomaly detection |
| Auth bypass | Logged-in users skip all limits |
| Failure mode | Fail open (never block on errors) |
| Audit | Every request logged to PostgreSQL |
