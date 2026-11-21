--
-- FAIRPASS: Complete Local Offline Database Schema (Fresh Installation)
-- Database: fairpass
-- Compatible with PostgreSQL 15+
--
-- This schema represents the full 12-table local offline database:
--  1. edge_events
--  2. gate_devices
--  3. event_gates
--  4. event_gate_devices
--  5. ticket_manifest
--  6. gate_scans
--  7. iot_readings
--  8. sync_outbox
--  9. manifest_metadata
-- 10. sync_checkpoints
-- 11. revoked_tickets
-- 12. device_credentials

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================================
-- 1. edge_events
-- ============================================================================
CREATE TABLE public.edge_events (
    id UUID NOT NULL PRIMARY KEY,
    event_name CHARACTER VARYING(255) NOT NULL,
    manifest_version INTEGER DEFAULT 1 NOT NULL,
    valid_from TIMESTAMP WITH TIME ZONE NOT NULL,
    valid_until TIMESTAMP WITH TIME ZONE NOT NULL,
    manifest_signature TEXT NOT NULL,
    downloaded_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    CONSTRAINT edge_events_check CHECK ((valid_until > valid_from))
);

-- ============================================================================
-- 2. gate_devices
-- ============================================================================
CREATE TABLE public.gate_devices (
    id UUID DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    device_identifier CHARACTER VARYING(100) NOT NULL UNIQUE,
    device_name CHARACTER VARYING(150) NOT NULL,
    gate_id CHARACTER VARYING(100) NOT NULL,
    device_secret_hash TEXT NOT NULL,
    device_status CHARACTER VARYING(20) DEFAULT 'ACTIVE'::CHARACTER VARYING,
    last_seen TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    CONSTRAINT gate_devices_device_status_check CHECK (((device_status)::text = ANY ((ARRAY['ACTIVE'::CHARACTER VARYING, 'INACTIVE'::CHARACTER VARYING, 'REVOKED'::CHARACTER VARYING])::text[])))
);

-- ============================================================================
-- 3. event_gates
-- ============================================================================
CREATE TABLE public.event_gates (
    id UUID NOT NULL PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES public.edge_events(id) ON DELETE CASCADE,
    gate_code CHARACTER VARYING(100) NOT NULL,
    gate_name CHARACTER VARYING(150) NOT NULL,
    gate_status CHARACTER VARYING(20) DEFAULT 'ACTIVE'::CHARACTER VARYING NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    CONSTRAINT event_gates_status_check CHECK (((gate_status)::text = ANY ((ARRAY['ACTIVE'::CHARACTER VARYING, 'INACTIVE'::CHARACTER VARYING])::text[]))),
    CONSTRAINT event_gates_event_gate_code_key UNIQUE (event_id, gate_code),
    CONSTRAINT event_gates_id_event_id_key UNIQUE (id, event_id)
);

CREATE INDEX idx_event_gates_event ON public.event_gates USING btree (event_id);

-- ============================================================================
-- 4. event_gate_devices
-- ============================================================================
CREATE TABLE public.event_gate_devices (
    id UUID DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    event_id UUID NOT NULL,
    event_gate_id UUID NOT NULL,
    device_id UUID NOT NULL REFERENCES public.gate_devices(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    revoked_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT event_gate_devices_gate_fkey FOREIGN KEY (event_gate_id, event_id) REFERENCES public.event_gates(id, event_id) ON DELETE CASCADE,
    CONSTRAINT event_gate_devices_check CHECK ((revoked_at IS NULL OR revoked_at >= assigned_at))
);

CREATE UNIQUE INDEX idx_unique_active_device_event ON public.event_gate_devices USING btree (event_id, device_id) WHERE (revoked_at IS NULL);
CREATE INDEX idx_event_gate_devices_lookup ON public.event_gate_devices USING btree (event_gate_id, device_id);

-- ============================================================================
-- 5. ticket_manifest
-- ============================================================================
CREATE TABLE public.ticket_manifest (
    ticket_id UUID NOT NULL PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES public.edge_events(id) ON DELETE CASCADE,
    assigned_gate_id UUID,
    ticket_hash CHARACTER(64) NOT NULL UNIQUE,
    owner_wallet CHARACTER VARYING(100) NOT NULL,
    ticket_status CHARACTER VARYING(20) DEFAULT 'ACTIVE'::CHARACTER VARYING NOT NULL,
    manifest_version INTEGER NOT NULL,
    valid_until TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    CONSTRAINT ticket_manifest_ticket_status_check CHECK (((ticket_status)::text = ANY ((ARRAY['ACTIVE'::CHARACTER VARYING, 'USED'::CHARACTER VARYING, 'CANCELLED'::CHARACTER VARYING])::text[]))),
    CONSTRAINT ticket_manifest_gate_event_fkey FOREIGN KEY (assigned_gate_id, event_id) REFERENCES public.event_gates(id, event_id)
);

CREATE INDEX idx_manifest_event ON public.ticket_manifest USING btree (event_id);
CREATE INDEX idx_ticket_assigned_gate ON public.ticket_manifest USING btree (assigned_gate_id);

-- ============================================================================
-- 6. gate_scans
-- ============================================================================
CREATE TABLE public.gate_scans (
    id UUID DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    ticket_id UUID REFERENCES public.ticket_manifest(ticket_id) ON DELETE SET NULL,
    device_id UUID NOT NULL REFERENCES public.gate_devices(id),
    event_id UUID,
    event_gate_id UUID,
    scan_result CHARACTER VARYING(30) NOT NULL,
    accepted BOOLEAN DEFAULT false NOT NULL,
    scanned_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    sync_status CHARACTER VARYING(20) DEFAULT 'PENDING'::CHARACTER VARYING NOT NULL,
    CONSTRAINT gate_scans_scan_result_check CHECK (((scan_result)::text = ANY ((ARRAY[
        'VALID'::CHARACTER VARYING,
        'DUPLICATE'::CHARACTER VARYING,
        'INVALID'::CHARACTER VARYING,
        'EXPIRED'::CHARACTER VARYING,
        'CANCELLED'::CHARACTER VARYING,
        'WRONG_GATE'::CHARACTER VARYING,
        'REVOKED'::CHARACTER VARYING,
        'DEVICE_UNAUTHORIZED'::CHARACTER VARYING
    ])::text[]))),
    CONSTRAINT gate_scans_sync_status_check CHECK (((sync_status)::text = ANY ((ARRAY['PENDING'::CHARACTER VARYING, 'SYNCED'::CHARACTER VARYING, 'FAILED'::CHARACTER VARYING])::text[]))),
    CONSTRAINT gate_scans_gate_event_fkey FOREIGN KEY (event_gate_id, event_id) REFERENCES public.event_gates(id, event_id)
);

CREATE INDEX idx_gate_scans_sync ON public.gate_scans USING btree (sync_status);
CREATE INDEX idx_gate_scans_time ON public.gate_scans USING btree (scanned_at);
CREATE INDEX idx_gate_scans_event_gate ON public.gate_scans USING btree (event_id, event_gate_id);
CREATE UNIQUE INDEX unique_accepted_ticket ON public.gate_scans USING btree (ticket_id) WHERE (accepted = true);

-- ============================================================================
-- 7. iot_readings
-- ============================================================================
CREATE TABLE public.iot_readings (
    id UUID DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES public.gate_devices(id),
    gate_id CHARACTER VARYING(100) NOT NULL,
    reading_type CHARACTER VARYING(50) NOT NULL,
    sensor_data JSONB DEFAULT '{}'::JSONB NOT NULL,
    recorded_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL
);

CREATE INDEX idx_iot_device_time ON public.iot_readings USING btree (device_id, recorded_at);

-- ============================================================================
-- 8. sync_outbox
-- ============================================================================
CREATE TABLE public.sync_outbox (
    id UUID DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    record_type CHARACTER VARYING(50) NOT NULL,
    record_id UUID NOT NULL,
    payload JSONB NOT NULL,
    sync_status CHARACTER VARYING(20) DEFAULT 'PENDING'::CHARACTER VARYING NOT NULL,
    retry_count INTEGER DEFAULT 0 NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    synced_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT sync_outbox_sync_status_check CHECK (((sync_status)::text = ANY ((ARRAY['PENDING'::CHARACTER VARYING, 'SYNCED'::CHARACTER VARYING, 'FAILED'::CHARACTER VARYING])::text[])))
);

CREATE INDEX idx_sync_pending ON public.sync_outbox USING btree (created_at) WHERE ((sync_status)::text = 'PENDING'::text);

-- ============================================================================
-- 9. manifest_metadata
-- ============================================================================
CREATE TABLE public.manifest_metadata (
    id UUID DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES public.edge_events(id) ON DELETE CASCADE,
    manifest_version INTEGER NOT NULL,
    manifest_hash TEXT NOT NULL,
    signature TEXT NOT NULL,
    signer_key_id CHARACTER VARYING(100) DEFAULT 'neon-global-signer-1'::CHARACTER VARYING NOT NULL,
    issued_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    downloaded_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    PRIMARY KEY (event_id, manifest_version)
);

CREATE INDEX idx_manifest_event_version ON public.manifest_metadata USING btree (event_id, manifest_version);

-- ============================================================================
-- 10. sync_checkpoints
-- ============================================================================
CREATE TABLE public.sync_checkpoints (
    stream_name CHARACTER VARYING(100) NOT NULL PRIMARY KEY,
    last_sequence BIGINT DEFAULT 0 NOT NULL,
    last_cursor TEXT,
    last_successful_sync TIMESTAMP WITH TIME ZONE,
    status CHARACTER VARYING(50) DEFAULT 'IDLE'::CHARACTER VARYING NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL
);

-- ============================================================================
-- 11. revoked_tickets
-- ============================================================================
CREATE TABLE public.revoked_tickets (
    event_id UUID NOT NULL REFERENCES public.edge_events(id) ON DELETE CASCADE,
    ticket_id UUID NOT NULL,
    revocation_sequence BIGINT NOT NULL,
    reason TEXT,
    revoked_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    revocation_signature TEXT NOT NULL,
    PRIMARY KEY (event_id, ticket_id)
);

CREATE INDEX idx_revoked_ticket ON public.revoked_tickets USING btree (ticket_id);
CREATE INDEX idx_revocation_sequence ON public.revoked_tickets USING btree (revocation_sequence);

-- ============================================================================
-- 12. device_credentials
-- ============================================================================
CREATE TABLE public.device_credentials (
    device_identifier CHARACTER VARYING(100) NOT NULL PRIMARY KEY REFERENCES public.gate_devices(device_identifier) ON DELETE CASCADE,
    gate_id CHARACTER VARYING(100) NOT NULL,
    public_key TEXT NOT NULL,
    credential_signature TEXT NOT NULL,
    issuer_key_id CHARACTER VARYING(100) DEFAULT 'neon-global-issuer-1'::CHARACTER VARYING NOT NULL,
    issued_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX idx_device_gate ON public.device_credentials USING btree (gate_id);
CREATE INDEX idx_device_expiry ON public.device_credentials USING btree (expires_at);
