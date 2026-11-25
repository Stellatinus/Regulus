-- regulus:reader-min 15
-- API tokens (#406, docs/authentication.md): credentials for programs, made by
-- a user, that can do less than their owner. Only the SHA-256 of the secret is
-- stored; prefix is its first eight characters, to tell tokens apart in a
-- list. permissions is a comma separated list of permission ids. A token has
-- no expiry when expires_at is null. Times are text in the fixed UTC format of
-- the other tables. Deleting the user deletes their tokens.
--
-- It only adds a table, so the previous release starts on a database that has
-- it (docs/updates.md).
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    permissions  TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    expires_at   TEXT,
    last_used_at TEXT,
    last_used_ip TEXT    NOT NULL DEFAULT '',
    revoked_at   TEXT
) STRICT;

CREATE INDEX api_tokens_by_user ON api_tokens (user_id);
