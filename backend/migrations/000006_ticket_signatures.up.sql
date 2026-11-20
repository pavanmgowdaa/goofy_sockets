-- Additive ticket integrity metadata. Existing rows remain version 0 and keep
-- their existing ticket_hash / QR credential semantics.
ALTER TABLE tickets ADD COLUMN ticket_signature text NOT NULL DEFAULT '';
ALTER TABLE tickets ADD COLUMN integrity_version smallint NOT NULL DEFAULT 0 CHECK (integrity_version IN (0,1));
