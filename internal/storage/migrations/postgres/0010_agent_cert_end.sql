-- When the client certificate of an enrolled agent ends (#245), so the hub can
-- show it and warn before it does. Written at enrollment; for an agent enrolled
-- before this column existed it stays NULL until the agent next connects and
-- the hub reads it from the certificate the agent presents.
ALTER TABLE agents ADD COLUMN cert_not_after TEXT;
