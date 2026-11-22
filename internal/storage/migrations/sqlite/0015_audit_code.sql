-- A code and arguments next to the text of an audit entry (#368), so the
-- dashboard can show the detail in the viewer's language without reading
-- English words. Both are empty for an entry written before this column
-- existed, or by a place that has no code yet; the English text stays.
-- detail_args is a JSON object of strings.
ALTER TABLE audit_log ADD COLUMN detail_code TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN detail_args TEXT NOT NULL DEFAULT '';
