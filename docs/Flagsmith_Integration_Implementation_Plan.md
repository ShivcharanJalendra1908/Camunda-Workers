# Flagsmith Feature Flags Integration Guide
### Go Backend & React Vite Frontend Architecture

This document serves as the master integration guide for the **Lemici Platform**. It translates the Node.js/Python guide provided in `Flagsmith_Backend_Guide_Shivcharan.docx` into our actual tech stack: **Go (Gin-Gonic) backend** and **Vite (React + TypeScript) frontend**.

---

## 👥 Contacts & Responsibilities
*   **Manoj Patil (Task Owner):** Provides flag names, environment SDK server keys (`ser.***`), and client keys.
*   **Gintesh / Syed:** Implementation guidance and support.
*   **Ankit (Project Lead):** Code review and final sign-off.

---

## 🛠️ Tech Stack Mapping
| Original Docx Concept | Go Backend Implementation (Completed) | React Vite Frontend Guide |
| :--- | :--- | :--- |
| **Node.js/Python SDK** | Official `github.com/Flagsmith/flagsmith-go-client/v3` | Official `@flagsmith/react` SDK |
| **Local Evaluation** | Enabled via `flagsmithapi.WithLocalEvaluation(ctx)` | Client-side cached flags |
| **Route Middleware** | Custom Gin Middleware: `middleware.RequireFlag("flag_name")` | React Guard Component / Hook |
| **User Identification** | Authenticated Identity (`userId` context claim) | User ID logged via Keycloak session |

---

## 🚀 Backend Integration (What We Have Implemented)

We have successfully set up the backend structure for Flagsmith. The following changes are live in the codebase:

### 1. Configuration Setup
We registered Flagsmith configuration variables within the master Go config structures:
*   **[config.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/internal/common/config/config.go)**: Created the `FlagsmithConfig` struct mapping directly to Viper configurations.
*   **[loader.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/internal/common/config/loader.go)**: Added automatic default values (e.g. 60-second polling refresh interval) and support for environment overrides (`FLAGSMITH_SERVER_KEY` and `FLAGSMITH_ENABLED`).
*   **[config.yaml](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/configs/config.yaml)**: Appended a reusable yaml block under top-level parameters:
    ```yaml
    flagsmith:
      enabled: false
      environment_key: "${FLAGSMITH_SERVER_KEY:}"
      enable_local_evaluation: true
      environment_refresh_ttl: 60
    ```
*   **[.env](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/.env)**: Added placeholders for clean environment variable management:
    ```bash
    # --- Flagsmith Feature Flags Configuration ---
    FLAGSMITH_ENABLED=false
    FLAGSMITH_SERVER_KEY=ser.your_flagsmith_server_sdk_key_here
    ```

### 2. Flagsmith Client Wrapper
*   **[client.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/internal/common/flagsmith/client.go)**: Created a wrapper service around `flagsmith-go-client/v3` that handles:
    *   **Fail-Safe / Fail-Open Behavior**: Evaluates flags gracefully without crashing if Flagsmith is offline or disabled.
    *   **Local Evaluation**: Runs a background goroutine to cache environment configs and segment rules, keeping request latency under sub-milliseconds.
    *   **Custom Identity Flags**: Integrates user-specific segment matching via identifiers and custom traits (like subscription tiers or roles).

### 3. Reusable Gin Route Middleware
*   **[flagsmith.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/internal/api/middleware/flagsmith.go)**: Implemented a reusable Gin middleware:
    *   Guards public/anonymous routes via global flag evaluations.
    *   Guards protected endpoints by parsing user identity (`userId`) and custom traits (like `email`, `subscription_tier`, and `roles` from the session/JWT context) and evaluating individual segment overrides.

### 4. Application Boot Initialization
*   **[main.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/cmd/api-gateway/main.go)**: Registered and initialized the Flagsmith client during the API Gateway boot sequence.

---

## 💡 How to Use Backend Feature Flags

Here are direct code examples of how to consume feature flags in Go.

### Scenario A: Protecting an API Gateway Route (Middleware)
You can guard any endpoint directly in [main.go](file:///c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/cmd/api-gateway/main.go):

```go
// Enforce that only users/environments with "ai_advanced_search" enabled can access this route
protectedAPI.POST("/ai/super-search", 
    middleware.RequireFlag("ai_advanced_search"), 
    workflowHandler.StartAISuperSearch,
)
```

### Scenario B: Checking Flags in Handlers or Workers (Business Logic)
You can check a flag's status inside any handler file dynamically:

```go
import "camunda-workers/internal/common/flagsmith"

func HandleFranchiseList(c *gin.Context) {
    ctx := c.Request.Context()
    userID := middleware.GetUserID(c)
    
    // Evaluate if AI recommendations are enabled for this specific user
    isAIEnabled := flagsmith.IsFeatureEnabledForUser(ctx, userID, "ai_recommendations", map[string]interface{}{
        "subscription_tier": c.GetString("subscriptionTier"),
    })
    
    if isAIEnabled {
        // Fetch personalized AI recommendation listings
    } else {
        // Fetch standard generic listings
    }
}
```

---

## 🎨 React Vite Frontend Integration Plan

To implement Flagsmith on the **Vite + React (TypeScript)** frontend, the frontend team should follow this clean workflow:

### Step 1: Install SDK
In the `frontend` directory, install the official Flagsmith React library:
```bash
npm install flagsmith-react --save
```

### Step 2: Initialize Provider
Wrap your root application component (typically `src/main.tsx` or `src/App.tsx`) with `FlagsmithProvider` using the **Client-side Environment Key** (ask Manoj Patil for this key):

```tsx
// src/main.tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import flagsmith from 'flagsmith';
import { FlagsmithProvider } from 'flagsmith/react';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <FlagsmithProvider
      flagsmith={flagsmith}
      options={{
        environmentID: "YOUR_CLIENT_SIDE_ENVIRONMENT_KEY", // starts with client key format
        // Optionally bind user identity immediately if logged in
        identity: localStorage.getItem('userId') || undefined,
        traits: {
          subscription_tier: localStorage.getItem('subscriptionTier') || 'free',
        }
      }}
    >
      <App />
    </FlagsmithProvider>
  </React.StrictMode>
);
```

### Step 3: Identify Authenticated Users
When users log in or change their subscription tier, update their Flagsmith identity to receive segments:

```tsx
// src/components/Login.tsx
import { useFlagsmith } from 'flagsmith/react';

export const LoginButton = () => {
  const flagsmith = useFlagsmith();

  const handleLoginSuccess = (user: { id: string; email: string; tier: string }) => {
    // Identify user in Flagsmith
    flagsmith.identify(user.id, {
      email: user.email,
      subscription_tier: user.tier,
    });
  };
};
```

### Step 4: Hide/Show UI Elements (Hooks)
Consume feature flags in functional React components using the `useFlags` hook:

```tsx
// src/components/AISearchCard.tsx
import React from 'react';
import { useFlags } from 'flagsmith/react';

export const AISearchCard: React.FC = () => {
  // Check feature flags
  const flags = useFlags(['ai_enhanced_responses', 'premium_ai_search']);

  return (
    <div className="search-card">
      <h2>Franchise Search</h2>
      <input type="text" placeholder="Search standard franchises..." />
      
      {/* Show standard AI helper if enabled */}
      {flags.ai_enhanced_responses.enabled && (
        <span className="ai-badge">AI Powered</span>
      )}
      
      {/* Expose Premium Search Input only if flag is enabled */}
      {flags.premium_ai_search.enabled ? (
        <button className="premium-search-btn">Run Premium AI Matchmaker</button>
      ) : (
        <div className="upgrade-prompt">
          Upgrade to Premium for AI Matchmaking
        </div>
      )}
    </div>
  );
};
```

---

## ⚠️ Checklist for Manoj Patil & Ankit

Before enabling Flagsmith in production:
1. [ ] **Manoj Patil**: Provide `FLAGSMITH_SERVER_KEY` (starts with `ser.`) for the backend.
2. [ ] **Manoj Patil**: Provide `FLAGSMITH_CLIENT_KEY` (starts with `cli.`) for the React frontend.
3. [ ] **Syed / Gintesh**: Set up matching flag keys in the Flagsmith dashboard (e.g. `ai_enhanced_responses`, `premium_ai_search`).
4. [ ] **Development Team**: Enable the integration in `.env` by setting `FLAGSMITH_ENABLED=true` in local development.
