
-- FAIRPASS Local Migration 002
-- Gate allocation and offline QR support.
-- Preserves existing local table structures and TEXT identifiers.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 1. Event gates
CREATE TABLE IF NOT EXISTS public.event_gates (
    id UUID PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES public.edge_events(id) ON DELETE CASCADE,
    gate_code VARCHAR(100) NOT NULL,
    gate_name VARCHAR(150) NOT NULL,
    gate_status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT event_gates_status_check
        CHECK (gate_status IN ('ACTIVE', 'INACTIVE')),

    CONSTRAINT event_gates_event_gate_code_key
        UNIQUE (event_id, gate_code),

    CONSTRAINT event_gates_id_event_id_key
        UNIQUE (id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_event_gates_event
ON public.event_gates(event_id);


-- 2. Event gate device assignments
CREATE TABLE IF NOT EXISTS public.event_gate_devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL,
    event_gate_id UUID NOT NULL,
    device_id UUID NOT NULL REFERENCES public.gate_devices(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,

    CONSTRAINT event_gate_devices_gate_fkey
        FOREIGN KEY (event_gate_id, event_id)
        REFERENCES public.event_gates(id, event_id) ON DELETE CASCADE,

    CONSTRAINT event_gate_devices_check
        CHECK (revoked_at IS NULL OR revoked_at >= assigned_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_active_device_event
ON public.event_gate_devices(event_id, device_id)
WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_event_gate_devices_lookup
ON public.event_gate_devices(event_gate_id, device_id);


-- 3. Ticket manifest: gate assignment and QR hash
ALTER TABLE public.ticket_manifest
ADD COLUMN IF NOT EXISTS assigned_gate_id UUID;

ALTER TABLE public.ticket_manifest
ADD COLUMN IF NOT EXISTS qr_token_hash TEXT;

-- Preserve existing hashes as a temporary compatibility fallback.
UPDATE public.ticket_manifest
SET qr_token_hash = ticket_hash::TEXT
WHERE qr_token_hash IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ticket_qr_token_hash
ON public.ticket_manifest(qr_token_hash)
WHERE qr_token_hash IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'ticket_manifest_gate_event_fkey'
          AND conrelid = 'public.ticket_manifest'::regclass
    ) THEN
        ALTER TABLE public.ticket_manifest
        ADD CONSTRAINT ticket_manifest_gate_event_fkey
        FOREIGN KEY (assigned_gate_id, event_id)
        REFERENCES public.event_gates(id, event_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_ticket_assigned_gate
ON public.ticket_manifest(assigned_gate_id);


-- 4. Gate scans: event and gate references
ALTER TABLE public.gate_scans
ADD COLUMN IF NOT EXISTS event_id UUID,
ADD COLUMN IF NOT EXISTS event_gate_id UUID;

ALTER TABLE public.gate_scans
DROP CONSTRAINT IF EXISTS gate_scans_scan_result_check;

ALTER TABLE public.gate_scans
ADD CONSTRAINT gate_scans_scan_result_check
CHECK (
    scan_result IN (
        'VALID',
        'DUPLICATE',
        'INVALID',
        'EXPIRED',
        'CANCELLED',
        'WRONG_GATE',
        'REVOKED',
        'DEVICE_UNAUTHORIZED'
    )
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'gate_scans_gate_event_fkey'
          AND conrelid = 'public.gate_scans'::regclass
    ) THEN
        ALTER TABLE public.gate_scans
        ADD CONSTRAINT gate_scans_gate_event_fkey
        FOREIGN KEY (event_gate_id, event_id)
        REFERENCES public.event_gates(id, event_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_gate_scans_event_gate
ON public.gate_scans(event_id, event_gate_id);


-- 5. Existing manifest_metadata is deliberately preserved.
-- Its existing TEXT event_id, primary key and extra columns remain unchanged.


-- 6. Existing sync_checkpoints is deliberately preserved.


-- 7. Existing revoked_tickets is deliberately preserved.
-- In particular, keep TEXT identifiers, revocation_reason and signer_key_id.


-- 8. Existing device_credentials is deliberately preserved.
-- Keep its existing revoked_at and created_at columns.


COMMIT;
