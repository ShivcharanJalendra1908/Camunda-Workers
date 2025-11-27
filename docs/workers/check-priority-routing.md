
---

## 📁 **11. `docs/workers/check-priority-routing.md`**

```markdown
# Check Priority Routing Worker

## Purpose
Determines application routing priority based on franchisor account type.

## Task Type
`check-priority-routing`

## Input Schema
```json
{
  "franchiseId": "string"
}

## Output Schema
```json
{
  "isPremiumFranchisor": "boolean",
  "routingPriority": "string (high|medium|low)"
}