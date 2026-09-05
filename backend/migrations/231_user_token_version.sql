-- A zero counter preserves existing password-fingerprint JWT versions.
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version BIGINT NOT NULL DEFAULT 0;
