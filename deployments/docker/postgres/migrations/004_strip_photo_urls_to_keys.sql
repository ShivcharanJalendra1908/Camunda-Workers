-- ============================================================
-- MIGRATION: Convert profile_image from full S3 URL to S3 key
-- Phase 4 of Photo Storage Migration
--
-- Run AFTER Phase 1-3 code is deployed (handlers construct
-- CDN URL from key at read time).
--
-- This migration strips the scheme + host from stored URLs,
-- keeping only the S3 object key.
--
-- BEFORE: https://lemici-profile-photos.s3.amazonaws.com/profile-photos/abc/avatar.jpg
-- AFTER:  profile-photos/abc/avatar.jpg
-- ============================================================

-- Step 1: Convert full URLs to keys
UPDATE users
SET profile_image = REGEXP_REPLACE(
    profile_image,
    '^https?://[^/]+/',
    ''
)
WHERE profile_image IS NOT NULL
  AND profile_image LIKE 'http%';

-- Step 2: Verify no full URLs remain
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM users
        WHERE profile_image LIKE 'http%'
    ) THEN
        RAISE EXCEPTION 'Migration failed: full URLs still present in users.profile_image';
    END IF;
END $$;

-- Step 3: Update column comment
COMMENT ON COLUMN users.profile_image IS 'S3 object key (not full URL). CDN URL constructed at read time via BuildPhotoURL().';
