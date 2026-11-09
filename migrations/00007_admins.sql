-- +goose Up
-- Admin accounts (cmd/admin), separate from readers' users.
CREATE TABLE admins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email citext NOT NULL UNIQUE,
    hashed_password BYTEA NOT NULL,  -- internal/password (Argon2id)
    totp_secret BYTEA NOT NULL,      -- AES-256-GCM ciphertext (nonce prepended) of the 20-byte secret, sealed with -secret-key
    totp_last_step BIGINT NOT NULL DEFAULT 0, -- the last 30-second step a code was accepted for, so each code works once
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_recovery_codes (
    admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    hash BYTEA NOT NULL, -- sha256 of the code; deleted when used
    PRIMARY KEY (admin_id, hash)
);

-- The admin's scs sessions, apart from readers' sessions table.
CREATE TABLE admin_sessions (
    token TEXT PRIMARY KEY,
    data BYTEA NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX admin_sessions_expiry_idx ON admin_sessions (expiry);

-- +goose Down
DROP TABLE admin_sessions;
DROP TABLE admin_recovery_codes;
DROP TABLE admins;
