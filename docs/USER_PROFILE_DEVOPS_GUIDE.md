# User Profile — DevOps Deployment Guide

> **Version:** 1.0
> **Last Updated:** 2026-07-14
> **Scope:** S3, CloudFront, Kong, environment variables, database migration

---

## 1. S3 Bucket Setup

### Bucket: `lemici-profile-photos`

```bash
aws s3api create-bucket \
  --bucket lemici-profile-photos \
  --region ap-south-1 \
  --create-bucket-configuration LocationConstraint=ap-south-1
```

### CORS Configuration

```json
{
  "CORSRules": [
    {
      "AllowedHeaders": ["*"],
      "AllowedMethods": ["GET", "PUT", "POST", "DELETE", "HEAD"],
      "AllowedOrigins": [
        "https://www.lemici.com",
        "https://lemici.com",
        "https://dev.lemici.com",
        "https://d3c34598mt7qdx.cloudfront.net",
        "http://localhost:3000"
      ],
      "ExposeHeaders": ["ETag", "Content-Length", "Content-Type"],
      "MaxAgeSeconds": 3600
    }
  ]
}
```

Apply CORS:
```bash
aws s3api put-bucket-cors \
  --bucket lemici-profile-photos \
  --cors-file://s3-cors.json
```

### Bucket Policy (Public Read via CloudFront)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowCloudFrontServicePrincipal",
      "Effect": "Allow",
      "Principal": {
        "Service": "cloudfront.amazonaws.com"
      },
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::lemici-profile-photos/profile-photos/*"
    }
  ]
}
```

### Lifecycle Rules

| Rule | Description |
|---|---|
| Orphaned objects | Expire after 30 days (safety net for orphaned uploads) |
| Incomplete multipart | Abort after 1 day |

```bash
aws s3api put-bucket-lifecycle-configuration \
  --bucket lemici-profile-photos \
  --lifecycle-configuration '{
    "Rules": [
      {
        "ID": "abort-incomplete-multipart",
        "Status": "Enabled",
        "AbortIncompleteMultipartUpload": {
          "DaysAfterInitiation": 1
        }
      }
    ]
  }'
```

### Block Public Access

```bash
aws s3api put-public-access-block \
  --bucket lemici-profile-photos \
  --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
```

---

## 2. CloudFront Distribution

### Distribution: `d3c34598mt7qdx.cloudfront.net`

**Already exists** — verify configuration matches:

| Setting | Value |
|---|---|
| Origin | `lemici-profile-photos.s3.ap-south-1.amazonaws.com` |
| Origin Access | OAI (CloudFront → S3) |
| Price Class | PriceClass_100 (India + US + EU) |
| Viewer Protocol Policy | Redirect HTTP to HTTPS |
| Cache Policy | CachingOptimized (default) |
| TTL | 31536000 (1 year) |
| Alternate Domain | (none — use default .cloudfront.net) |

### Cache Behavior for `/profile-photos/*`

| Setting | Value |
|---|---|
| Path Pattern | `/profile-photos/*` |
| Origin | S3 bucket |
| Cache Policy | CachingOptimized |
| Compress Objects | Yes (gzip, br) |

### Invalidating Cache

When photos are updated, CloudFront cache is invalidated by the `Cache-Control: max-age=31536000, immutable` header on S3 objects. Old photos are deleted, so no invalidation needed.

If manual invalidation is required:
```bash
aws cloudfront create-invalidation \
  --distribution-id E1XXXXXXXXXXXX \
  --paths "/profile-photos/*"
```

---

## 3. Environment Variables

### Required (`.env` or environment)

```bash
# S3 Configuration
S3_ENABLED=true                    # Flip from false to true
S3_BUCKET=lemici-profile-photos
S3_REGION=ap-south-1
CDN_BASE_URL=https://d3c34598mt7qdx.cloudfront.net

# AWS Credentials (if not using IAM role)
AWS_ACCESS_KEY_ID=AKIAXXXXXXXXXXXXXXXX
AWS_SECRET_ACCESS_KEY=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
AWS_REGION=ap-south-1
```

### Photo Constraints (Defaults — Override via env if needed)

```bash
S3_MAX_FILE_SIZE=3145728           # 3MB in bytes
S3_MAX_WIDTH=200                   # pixels
S3_MAX_HEIGHT=200                  # pixels
S3_PRESIGN_TTL=3600                # seconds
```

### CORS Origins (API Gateway)

```bash
API_CORS_ALLOWED_ORIGINS=https://www.lemici.com,https://lemici.com,https://dev.lemici.com,https://d3c34598mt7qdx.cloudfront.net,http://localhost:3000
```

---

## 4. Kong Configuration

### User Routes — Request Size Limit

The `request-size-limiting` plugin on user routes is set to **3MB**:

```yaml
# configs/kong/kong.yaml (line 336-339)
- name: request-size-limiting
  config:
    allowed_payload_size: 3
    size_unit: megabytes
```

This matches the S3 max file size. **No change needed.**

### Rate Limiting — User Routes

```yaml
# configs/kong/kong.yaml (line 37-41)
- name: rate-limiting
  config:
    minute: 30
    policy: local
    limit_by: ip
```

**30 requests/minute per IP** on user profile routes.

### Required Kong Plugins

| Plugin | Config | Purpose |
|---|---|---|
| `request-size-limiting` | 3MB | Prevent oversized uploads |
| `rate-limiting` | 30/min | Prevent abuse |
| `bot-detection` | deny wget, python-requests | Block automated requests |
| `response-transformer` | Security headers | X-Frame-Options, CSP, etc. |

---

## 5. Database Migration

### Migration: `004_strip_photo_urls_to_keys.sql`

**Run AFTER code deployment (Phases 1-3 are live).**

```bash
psql -U postgres -d franchisehub -f deployments/docker/postgres/migrations/004_strip_photo_urls_to_keys.sql
```

### What It Does

1. Converts full S3 URLs to keys:
   - BEFORE: `https://lemici-profile-photos.s3.amazonaws.com/profile-photos/abc/avatar.jpg`
   - AFTER: `profile-photos/abc/avatar.jpg`
2. Verifies no full URLs remain (raises exception if any found)
3. Adds column comment: `profile_image` stores S3 key, not URL

### Rollback

If rollback is needed, re-construct URLs in the application layer. The `BuildPhotoURL()` function handles this automatically — no DB rollback needed.

### Post-Migration Verification

```sql
-- Verify no full URLs remain
SELECT COUNT(*) FROM users WHERE profile_image LIKE 'http%';
-- Expected: 0

-- Verify keys are valid format
SELECT profile_image FROM users WHERE profile_image IS NOT NULL LIMIT 5;
-- Expected: profile-photos/{uuid}/{uuid}.jpg

-- Verify column comment
SELECT obj_description(('users.profile_image')::regclass, 'pg_attribute');
```

---

## 6. Deployment Checklist

### Pre-Deployment

- [ ] S3 bucket `lemici-profile-photos` created with CORS
- [ ] CloudFront distribution configured and deployed
- [ ] S3 bucket policy allows CloudFront OAI
- [ ] Environment variables set (`S3_ENABLED=true`, `CDN_BASE_URL`)
- [ ] AWS credentials available (or IAM role configured)
- [ ] Kong `request-size-limiting` plugin at 3MB on user routes

### Code Deployment

- [ ] Deploy code (Phases 1-3 already live)
- [ ] Verify `BuildPhotoURL()` works: `GET /user/profile` returns CDN URL
- [ ] Verify photo upload: `PUT /user/profile` with multipart form works
- [ ] Verify S3 object has `Cache-Control: max-age=31536000, immutable`

### Post-Deployment

- [ ] Run migration `004_strip_photo_urls_to_keys.sql`
- [ ] Verify no full URLs remain in `users.profile_image`
- [ ] Test photo upload → S3 → CDN URL flow end-to-end
- [ ] Test old photo cleanup (upload new photo, verify old deleted)
- [ ] Test account deletion (verify S3 object deleted)

### Rollback Plan

1. Set `S3_ENABLED=false` in environment
2. Code gracefully handles S3 unavailable (returns 503 on photo upload)
3. Existing photos continue to serve from CloudFront
4. No database rollback needed

---

## 7. Monitoring & Alerts

### Key Metrics

| Metric | Source | Alert Threshold |
|---|---|---|
| S3 upload failures | CloudWatch `aws.s3.requests.error.5xx` | > 5 in 5 min |
| CloudFront 4xx/5xx | CloudWatch `cloudfront.requests` | > 10 in 5 min |
| Photo upload latency | Application logs | > 5 seconds |
| Orphaned S3 objects | S3 inventory | > 100 objects |

### Log Queries

```bash
# Check photo upload success rate
aws logs filter-log-events \
  --log-group-name /ecs/api-gateway \
  --filter-pattern "Profile photo uploaded" \
  --start-time $(date -d '1 hour ago' +%s)000

# Check S3 upload failures
aws logs filter-log-events \
  --log-group-name /ecs/api-gateway \
  --filter-pattern "S3 photo upload failed" \
  --start-time $(date -d '1 hour ago' +%s)000
```

---

## 8. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `PHOTO_STORAGE_UNAVAILABLE` | `S3_ENABLED=false` or missing AWS credentials | Set `S3_ENABLED=true`, verify AWS credentials |
| `PHOTO_TOO_LARGE` | File exceeds 3MB | Reduce file size or increase `S3_MAX_FILE_SIZE` |
| `PHOTO_UNSUPPORTED_FORMAT` | File not JPEG/PNG/WebP | Convert to supported format |
| `PHOTO_SECURITY_REJECTED` | EXIF/GPS data or suspicious content | Strip EXIF before upload |
| CDN URL returns 403 | CloudFront OAI not configured | Verify bucket policy allows CloudFront |
| CDN URL returns 404 | Object doesn't exist in S3 | Check `profile_image` column has S3 key |
| Slow uploads | S3 region far from users | Use `ap-south-1` for Indian users |
| Old photos not deleted | `launchOldPhotoCleanup` not firing | Check workflow success response has `oldProfileImage` |
