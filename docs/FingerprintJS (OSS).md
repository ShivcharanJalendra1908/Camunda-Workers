# Frontend Integration Guide: FingerprintJS (OSS)

**Version Context:** Q2 2026
**Audience:** Frontend Engineers (React / SPA / SSR)
**Library:** FingerprintJS (Open Source Edition)

---

## 1. Purpose

Integrate browser fingerprinting to generate a **stable, pseudonymous identifier (`visitorId`)** for anonymous users, enabling:

- Anonymous rate limiting
- Abuse detection (multi-session correlation)
- Identity continuity without login

---

## 2. What FingerprintJS Does

FingerprintJS generates a **deterministic hash** based on browser/device characteristics (“entropy sources”).

### Core Entropy Signals (as of 2026)

- Canvas rendering fingerprint
- WebGL vendor/renderer
- AudioContext fingerprint
- Installed fonts (via probing)
- Screen resolution & color depth
- Timezone & locale
- Hardware concurrency (CPU cores)
- Device memory (where available)
- Browser feature set (APIs, flags)

### Output

```json
{
  "visitorId": "a1b2c3d4e5f6...",
  "confidence": {
    "score": 0.97
  }
}
```

- `visitorId`: stable hash (primary identity signal)
- `confidence.score`: probability estimate of uniqueness

---

## 3. Important Constraints (Must Understand)

### Fingerprint Stability

| Scenario                  | Stability       |
| ------------------------- | --------------- |
| Page reload               | Stable          |
| Incognito mode            | New fingerprint |
| Different browser         | New fingerprint |
| Same device, same browser | Stable          |
| Anti-fingerprint browsers | Unstable        |

### Implication

Fingerprint **cannot be used alone** for enforcement. It must be combined with:

- cookie (`visitor_id`)
- backend reconciliation

---

## 4. Installation

### NPM

```bash
npm install @fingerprintjs/fingerprintjs
```

### CDN (fallback)

```html
<script src="https://openfpcdn.io/fingerprintjs/v4"></script>
```

---

## 5. Initialization Pattern (Recommended)

### Async Loader (Best Practice)

```javascript
import FingerprintJS from "@fingerprintjs/fingerprintjs";

let fpPromise = null;

export const loadFingerprint = () => {
  if (!fpPromise) {
    fpPromise = FingerprintJS.load({
      monitoring: false, // disable telemetry
    });
  }
  return fpPromise;
};
```

---

## 6. Generating Fingerprint

```javascript
export const getFingerprint = async () => {
  const fp = await loadFingerprint();
  const result = await fp.get();

  return {
    visitorId: result.visitorId,
    confidence: result.confidence.score,
  };
};
```

---

## 7. Integration with Backend Identity Flow

### Step 1 — On App Init

```javascript
const { visitorId } = await getFingerprint();

const res = await fetch("/identity/init", {
  method: "POST",
  headers: {
    "X-Fingerprint": visitorId,
  },
});

const data = await res.json();

localStorage.setItem("visitor_id", data.visitor_id);
```

---

### Step 2 — Attach to All API Calls

```javascript
const visitorId = localStorage.getItem("visitor_id");
const { visitorId: fingerprint } = await getFingerprint();

fetch("/api/resource", {
  headers: {
    "X-Visitor-Id": visitorId,
    "X-Fingerprint": fingerprint,
  },
});
```

---

## 8. Performance Considerations

### Cost Profile

| Operation | Cost      |
| --------- | --------- |
| `load()`  | ~20–80ms  |
| `get()`   | ~50–150ms |

### Optimization Strategy

- Load once per session
- Cache fingerprint in memory
- Avoid recomputation on every request

---

### Recommended Cache

```javascript
let cachedFingerprint = null;

export const getCachedFingerprint = async () => {
  if (cachedFingerprint) return cachedFingerprint;

  cachedFingerprint = await getFingerprint();
  return cachedFingerprint;
};
```

---

## 9. React Integration Pattern

### Hook

```javascript
import { useEffect, useState } from "react";

export const useFingerprint = () => {
  const [fp, setFp] = useState(null);

  useEffect(() => {
    let mounted = true;

    getCachedFingerprint().then((data) => {
      if (mounted) setFp(data);
    });

    return () => {
      mounted = false;
    };
  }, []);

  return fp;
};
```

---

## 10. SSR / Next.js Considerations

### Do NOT run on server

FingerprintJS relies on:

- `window`
- `navigator`
- browser APIs

### Safe usage

```javascript
useEffect(() => {
  getCachedFingerprint();
}, []);
```

---

## 11. Security Considerations

### Do not trust fingerprint blindly

Frontend can be spoofed:

- header manipulation
- script blocking
- modified browsers

### Always validate on backend:

- fingerprint ↔ visitor_id mapping
- anomaly detection

---

## 12. Privacy & Compliance (Important)

### Fingerprinting = Personal Data (in many jurisdictions)

- Considered **pseudonymous identifier**
- Falls under:
  - GDPR (EU)
  - DPDP (India, evolving enforcement)

### Required Actions

- Update privacy policy
- Declare fingerprint usage
- Provide opt-out (recommended)

---

## 13. Anti-Fingerprinting Environments

Expect degraded reliability in:

- Brave (strict mode)
- Tor Browser
- Firefox with resistFingerprinting
- Safari (aggressive entropy reduction)

### Handling Strategy

If `confidence.score < 0.5`:

- treat as low-trust
- rely more on backend signals

---

## 14. OSS vs Pro Version (Clarification)

| Feature                 | OSS    | Pro  |
| ----------------------- | ------ | ---- |
| Client-side fingerprint | Yes    | Yes  |
| Server correlation      | No     | Yes  |
| Bot detection           | No     | Yes  |
| Accuracy                | Medium | High |

For MVP: OSS is sufficient.

---

## 15. Failure Modes

| Issue                      | Cause                    |
| -------------------------- | ------------------------ |
| New identity every session | incognito                |
| Same user, different ID    | browser change           |
| Collisions                 | low entropy environments |

---

## 16. Best Practices (Frontend)

- Initialize once, reuse globally
- Never block UI on fingerprint generation
- Cache aggressively
- Always send fingerprint with API requests
- Combine with cookie-based identity

---

## 17. Minimal Integration Checklist

- [ ] Install library
- [ ] Implement async loader
- [ ] Generate fingerprint on app init
- [ ] Send fingerprint in `/identity/init`
- [ ] Attach fingerprint to all API calls
- [ ] Cache fingerprint in memory
- [ ] Handle SSR safely

---

## 18. Summary

FingerprintJS provides a **deterministic, device-level identifier** using browser entropy.

It is:

- useful for correlation
- insufficient for enforcement alone

Correct usage:

- combine with backend-issued `visitor_id`
- use as a supporting signal in identity graph

---

## 19. Recommended Integration Pattern (Final)

```text
Fingerprint → Backend Identity → Cookie(visitor_id)
           ↘ correlation layer (Redis)
```

This ensures:

- stability (cookie)
- resilience (fingerprint)
- extensibility (backend control)

---

## 20. Future Extensions (Optional)

- upgrade to FingerprintJS Pro
- add bot detection layer
- integrate CAPTCHA on low confidence
- add device graph tracking

---

End of Document
