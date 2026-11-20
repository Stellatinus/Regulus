-- Step-up re-authentication (#279): when the user of a session last proved
-- the password (and the second factor, when on). A login counts as a proof, so
-- a new session starts with it set; NULL, for a session created before this
-- column existed, means "never": the next sensitive action asks again.
ALTER TABLE sessions ADD COLUMN reauth_at TEXT;
