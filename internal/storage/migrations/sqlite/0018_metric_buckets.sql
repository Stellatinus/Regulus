-- regulus:reader-min 15
-- The stored metrics history (#405, docs/architecture.md): completed buckets
-- of the averaged reports, in two tiers. tier is the length of a bucket in
-- minutes (1 or 15) and start its first second (Unix time, a multiple of the
-- length). Memory and swap are bytes in use, with their totals so that a chart
-- can draw a share; the network rates are the sum over the physical
-- interfaces (the ones the dashboard counts) and null when no report of the
-- bucket had one; disk_pct is
-- the highest usage of any file system of the server, null when none was
-- reported. samples is the number of reports the bucket averages.
--
-- It only adds a table, so the previous release starts on a database that has
-- it (docs/updates.md).
CREATE TABLE metric_buckets (
    agent_id   TEXT    NOT NULL,
    tier       INTEGER NOT NULL CHECK (tier IN (1, 15)),
    start      INTEGER NOT NULL,
    samples    INTEGER NOT NULL,
    cpu        REAL,
    load1      REAL    NOT NULL,
    mem_used   INTEGER NOT NULL,
    mem_total  INTEGER NOT NULL,
    swap_used  INTEGER NOT NULL,
    swap_total INTEGER NOT NULL,
    rx_bps     REAL,
    tx_bps     REAL,
    disk_pct   REAL,
    PRIMARY KEY (agent_id, tier, start)
) STRICT, WITHOUT ROWID;

-- Pruning deletes by age across all agents.
CREATE INDEX metric_buckets_by_start ON metric_buckets (tier, start);
