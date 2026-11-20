-- Review signed ticket records before rolling this migration back.
ALTER TABLE tickets DROP COLUMN IF EXISTS ticket_signature;
ALTER TABLE tickets DROP COLUMN IF EXISTS integrity_version;
