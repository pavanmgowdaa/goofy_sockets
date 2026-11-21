CREATE INDEX IF NOT EXISTS idx_manifest_event_version
ON manifest_metadata (event_id, manifest_version);

CREATE INDEX IF NOT EXISTS idx_revoked_ticket
ON revoked_tickets (ticket_id);

CREATE INDEX IF NOT EXISTS idx_revocation_sequence
ON revoked_tickets (revocation_sequence);

CREATE INDEX IF NOT EXISTS idx_device_gate
ON device_credentials (gate_id);

CREATE INDEX IF NOT EXISTS idx_device_expiry
ON device_credentials (expires_at);