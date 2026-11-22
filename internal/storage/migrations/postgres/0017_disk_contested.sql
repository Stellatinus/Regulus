-- regulus:reader-min 15
-- The server that reports a disk another connected server also reports (#382).
-- Such a disk is a conflict, not a move: the row stays with the server that
-- had it and this column names the second one, until one of them stops
-- reporting the disk. It only adds a column with a default, so the previous
-- release starts on a database that has it (docs/updates.md).
ALTER TABLE disks ADD COLUMN contested_by TEXT NOT NULL DEFAULT '';
