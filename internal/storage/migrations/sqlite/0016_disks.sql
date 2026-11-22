-- regulus:reader-min 15
-- The physical disks the agents report, and the moves of a disk from one
-- server to another (#148). It only adds tables, so the previous release
-- starts on a database that has them (docs/updates.md).
--
-- identity_key is the identity of the disk (internal/diskid): it starts with
-- the kind of identifier it was made from. A disk that cannot be identified
-- has the key 'slot:<agent>:<device>' instead: it is tracked on that server
-- only, and identifiable is 0.
CREATE TABLE disks (
    identity_key TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    identifiable INTEGER NOT NULL CHECK (identifiable IN (0, 1)),
    conflict     INTEGER NOT NULL DEFAULT 0 CHECK (conflict IN (0, 1)),
    reason       TEXT NOT NULL DEFAULT '',
    agent_id     TEXT NOT NULL,
    device       TEXT NOT NULL DEFAULT '',
    vendor       TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    serial       TEXT NOT NULL DEFAULT '',
    wwn          TEXT NOT NULL DEFAULT '',
    transport    TEXT NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    is_virtual   INTEGER NOT NULL DEFAULT 0 CHECK (is_virtual IN (0, 1)),
    present      INTEGER NOT NULL DEFAULT 1 CHECK (present IN (0, 1)),
    first_seen   TEXT NOT NULL,
    last_seen    TEXT NOT NULL
) STRICT;

CREATE INDEX disks_by_agent ON disks (agent_id);

-- One row each time a disk that was known on a server is reported by another.
CREATE TABLE disk_moves (
    id           INTEGER PRIMARY KEY,
    at           TEXT NOT NULL,
    identity_key TEXT NOT NULL,
    from_agent   TEXT NOT NULL,
    to_agent     TEXT NOT NULL
) STRICT;
