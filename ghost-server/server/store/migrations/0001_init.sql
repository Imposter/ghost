-- Portable schema: runs unchanged on SQLite and PostgreSQL.
-- Timestamps are Unix milliseconds; JSON documents and lists are TEXT.

CREATE TABLE networks (
    name            TEXT PRIMARY KEY,
    pool            TEXT NOT NULL,
    isolation       TEXT NOT NULL DEFAULT 'none',
    policy          TEXT NOT NULL,
    policy_revision BIGINT NOT NULL DEFAULT 0,
    created_at      BIGINT NOT NULL
);

CREATE TABLE peers (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    network     TEXT NOT NULL REFERENCES networks(name),
    name        TEXT NOT NULL DEFAULT '',
    public_key  TEXT NOT NULL DEFAULT '',
    address     TEXT,
    roles       TEXT NOT NULL DEFAULT '[]',
    tags        TEXT NOT NULL DEFAULT '[]',
    labels      TEXT NOT NULL DEFAULT '{}',
    endpoints   TEXT NOT NULL DEFAULT '[]',
    ephemeral   BIGINT NOT NULL DEFAULT 0,
    auth_key_id TEXT NOT NULL DEFAULT '',
    health      TEXT NOT NULL DEFAULT '',
    health_at   BIGINT,
    created_at  BIGINT NOT NULL,
    last_seen   BIGINT,
    expires_at  BIGINT,
    revoked_at  BIGINT
);

CREATE UNIQUE INDEX peers_network_address ON peers (network, address);
CREATE INDEX peers_network ON peers (network);

CREATE TABLE auth_keys (
    id           TEXT PRIMARY KEY,
    key_hash     TEXT NOT NULL UNIQUE,
    network      TEXT NOT NULL REFERENCES networks(name),
    reusable     BIGINT NOT NULL DEFAULT 0,
    ephemeral    BIGINT NOT NULL DEFAULT 0,
    roles        TEXT NOT NULL DEFAULT '[]',
    tags         TEXT NOT NULL DEFAULT '[]',
    labels       TEXT NOT NULL DEFAULT '{}',
    peer_ttl_ms  BIGINT NOT NULL DEFAULT 0,
    uses         BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,
    last_used_at BIGINT,
    revoked_at   BIGINT
);

CREATE INDEX auth_keys_network ON auth_keys (network);

CREATE TABLE enrollments (
    code_hash  TEXT PRIMARY KEY,
    poll_hash  TEXT NOT NULL UNIQUE,
    network    TEXT NOT NULL REFERENCES networks(name),
    name       TEXT NOT NULL DEFAULT '',
    public_key TEXT NOT NULL DEFAULT '',
    labels     TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL,
    roles      TEXT NOT NULL DEFAULT '[]',
    tags       TEXT NOT NULL DEFAULT '[]',
    reason     TEXT NOT NULL DEFAULT '',
    peer_id    TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    decided_at BIGINT
);

CREATE INDEX enrollments_network_status ON enrollments (network, status);

CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    key_hash     TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL DEFAULT '',
    scopes       TEXT NOT NULL DEFAULT '[]',
    networks     TEXT NOT NULL DEFAULT '[]',
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT,
    last_used_at BIGINT,
    revoked_at   BIGINT
);

CREATE TABLE audit_events (
    id      TEXT PRIMARY KEY,
    ts      BIGINT NOT NULL,
    network TEXT NOT NULL DEFAULT '',
    actor   TEXT NOT NULL,
    action  TEXT NOT NULL,
    target  TEXT NOT NULL DEFAULT '',
    detail  TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX audit_events_ts ON audit_events (ts);
CREATE INDEX audit_events_network_ts ON audit_events (network, ts);
