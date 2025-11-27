### 📄 `docs/workers/validate-subscription.md`
```markdown

# Validate Subscription Worker

## Purpose
Validates user subscription tier and permissions.

## Task Type
`validate-subscription`

## Inputs
- `userId` (string, required)
- `subscriptionTier` (string, required)

## Outputs
- `isValid` (boolean)
- `tierLevel` (string)

## Error Codes
- `SUBSCRIPTION_INVALID`: User not found
- `SUBSCRIPTION_EXPIRED`: Subscription expired
- `SUBSCRIPTION_CHECK_FAILED`: Database error

## Configuration
```yaml
workers:
  validate-subscription:
    enabled: true
    max_jobs_active: 5
    timeout: 10