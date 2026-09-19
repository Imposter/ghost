-- Portable schema: runs unchanged on SQLite and PostgreSQL.
-- Timestamps are Unix milliseconds; JSON documents are stored as TEXT.

CREATE TABLE networks (
    name            TEXT PRIMARY KEY,
    pool            TEXT NOT NULL,
    policy          TEXT NOT NULL DEFAULT '{}',
    policy_revision BIGINT NOT NULL DEFAULT 0,
    created_at      BIGINT NOT NULL
);

CREATE TABLE devices (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    network    TEXT NOT NULL REFERENCES networks(name),
    role       TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    labels     TEXT NOT NULL DEFAULT '{}',
    public_key TEXT NOT NULL DEFAULT '',
    address    TEXT,
    created_at BIGINT NOT NULL,
    last_seen  BIGINT,
    revoked_at BIGINT
);

CREATE UNIQUE INDEX devices_network_address ON devices (network, address);
CREATE INDEX devices_network ON devices (network);

CREATE TABLE pairing_codes (
    code_hash  TEXT PRIMARY KEY,
    network    TEXT NOT NULL REFERENCES networks(name),
    role       TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    labels     TEXT NOT NULL DEFAULT '{}',
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    used_at    BIGINT,
    device_id  TEXT
);

CREATE INDEX pairing_codes_expires ON pairing_codes (expires_at);
