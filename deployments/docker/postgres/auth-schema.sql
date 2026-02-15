-- Project Keystone: users & identities
-- This migration is intentionally minimal and strict.

-- BEGIN;

-- CREATE TABLE users (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     email text NOT NULL,
--     email_verified boolean NOT NULL DEFAULT false,
--     status text NOT NULL DEFAULT 'active',
--     created_at timestamptz NOT NULL DEFAULT NOW(),
--     updated_at timestamptz NOT NULL DEFAULT NOW()
-- );

-- -- Enforce case-insensitive email uniqueness
-- CREATE UNIQUE INDEX users_email_lower_unique
-- ON users (LOWER(email));

-- CREATE TABLE identities (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
--     provider text NOT NULL,
--     provider_user_id text NOT NULL,
--     created_at timestamptz NOT NULL DEFAULT NOW(),
--     updated_at timestamptz NOT NULL DEFAULT NOW(),

--     CONSTRAINT identities_provider_unique
--         UNIQUE (provider, provider_user_id)
-- );

-- CREATE INDEX identities_user_id_idx
-- ON identities (user_id);

-- COMMIT;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique
ON users (LOWER(email));

CREATE TABLE IF NOT EXISTS identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    provider_user_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT identities_provider_unique
        UNIQUE (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS identities_user_id_idx
ON identities (user_id);