--
-- PostgreSQL database dump
--

\restrict ubejBz94KABZhF00llylYTUyvTW9XRcMbkUdByQMjrXw6jGYWjUUjUckP59nKFv

-- Dumped from database version 18.6
-- Dumped by pg_dump version 18.6

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: edge_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_events (
    id uuid NOT NULL,
    event_name character varying(255) NOT NULL,
    manifest_version integer DEFAULT 1 NOT NULL,
    valid_from timestamp with time zone NOT NULL,
    valid_until timestamp with time zone NOT NULL,
    manifest_signature text NOT NULL,
    downloaded_at timestamp with time zone DEFAULT now(),
    CONSTRAINT edge_events_check CHECK ((valid_until > valid_from))
);


--
-- Name: gate_devices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gate_devices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    device_identifier character varying(100) NOT NULL,
    device_name character varying(150) NOT NULL,
    gate_id character varying(100) NOT NULL,
    device_secret_hash text NOT NULL,
    device_status character varying(20) DEFAULT 'ACTIVE'::character varying,
    last_seen timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    CONSTRAINT gate_devices_device_status_check CHECK (((device_status)::text = ANY ((ARRAY['ACTIVE'::character varying, 'INACTIVE'::character varying, 'REVOKED'::character varying])::text[])))
);


--
-- Name: gate_scans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gate_scans (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    ticket_id uuid,
    device_id uuid NOT NULL,
    scan_result character varying(30) NOT NULL,
    accepted boolean DEFAULT false NOT NULL,
    scanned_at timestamp with time zone DEFAULT now() NOT NULL,
    sync_status character varying(20) DEFAULT 'PENDING'::character varying NOT NULL,
    CONSTRAINT gate_scans_scan_result_check CHECK (((scan_result)::text = ANY ((ARRAY['VALID'::character varying, 'DUPLICATE'::character varying, 'INVALID'::character varying, 'EXPIRED'::character varying, 'CANCELLED'::character varying])::text[]))),
    CONSTRAINT gate_scans_sync_status_check CHECK (((sync_status)::text = ANY ((ARRAY['PENDING'::character varying, 'SYNCED'::character varying, 'FAILED'::character varying])::text[])))
);


--
-- Name: iot_readings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.iot_readings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    device_id uuid NOT NULL,
    gate_id character varying(100) NOT NULL,
    reading_type character varying(50) NOT NULL,
    sensor_data jsonb DEFAULT '{}'::jsonb NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sync_outbox; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sync_outbox (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    record_type character varying(50) NOT NULL,
    record_id uuid NOT NULL,
    payload jsonb NOT NULL,
    sync_status character varying(20) DEFAULT 'PENDING'::character varying NOT NULL,
    retry_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    synced_at timestamp with time zone,
    CONSTRAINT sync_outbox_sync_status_check CHECK (((sync_status)::text = ANY ((ARRAY['PENDING'::character varying, 'SYNCED'::character varying, 'FAILED'::character varying])::text[])))
);


--
-- Name: ticket_manifest; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ticket_manifest (
    ticket_id uuid NOT NULL,
    event_id uuid NOT NULL,
    ticket_hash character(64) NOT NULL,
    owner_wallet character varying(100) NOT NULL,
    ticket_status character varying(20) DEFAULT 'ACTIVE'::character varying NOT NULL,
    manifest_version integer NOT NULL,
    valid_until timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now(),
    CONSTRAINT ticket_manifest_ticket_status_check CHECK (((ticket_status)::text = ANY ((ARRAY['ACTIVE'::character varying, 'USED'::character varying, 'CANCELLED'::character varying])::text[])))
);


--
-- Name: edge_events edge_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_events
    ADD CONSTRAINT edge_events_pkey PRIMARY KEY (id);


--
-- Name: gate_devices gate_devices_device_identifier_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gate_devices
    ADD CONSTRAINT gate_devices_device_identifier_key UNIQUE (device_identifier);


--
-- Name: gate_devices gate_devices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gate_devices
    ADD CONSTRAINT gate_devices_pkey PRIMARY KEY (id);


--
-- Name: gate_scans gate_scans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gate_scans
    ADD CONSTRAINT gate_scans_pkey PRIMARY KEY (id);


--
-- Name: iot_readings iot_readings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.iot_readings
    ADD CONSTRAINT iot_readings_pkey PRIMARY KEY (id);


--
-- Name: sync_outbox sync_outbox_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sync_outbox
    ADD CONSTRAINT sync_outbox_pkey PRIMARY KEY (id);


--
-- Name: ticket_manifest ticket_manifest_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_manifest
    ADD CONSTRAINT ticket_manifest_pkey PRIMARY KEY (ticket_id);


--
-- Name: ticket_manifest ticket_manifest_ticket_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_manifest
    ADD CONSTRAINT ticket_manifest_ticket_hash_key UNIQUE (ticket_hash);


--
-- Name: idx_gate_scans_sync; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_gate_scans_sync ON public.gate_scans USING btree (sync_status);


--
-- Name: idx_gate_scans_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_gate_scans_time ON public.gate_scans USING btree (scanned_at);


--
-- Name: idx_iot_device_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_iot_device_time ON public.iot_readings USING btree (device_id, recorded_at);


--
-- Name: idx_manifest_event; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_manifest_event ON public.ticket_manifest USING btree (event_id);


--
-- Name: idx_sync_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sync_pending ON public.sync_outbox USING btree (created_at) WHERE ((sync_status)::text = 'PENDING'::text);


--
-- Name: unique_accepted_ticket; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX unique_accepted_ticket ON public.gate_scans USING btree (ticket_id) WHERE (accepted = true);


--
-- Name: gate_scans gate_scans_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gate_scans
    ADD CONSTRAINT gate_scans_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.gate_devices(id);


--
-- Name: gate_scans gate_scans_ticket_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gate_scans
    ADD CONSTRAINT gate_scans_ticket_id_fkey FOREIGN KEY (ticket_id) REFERENCES public.ticket_manifest(ticket_id);


--
-- Name: iot_readings iot_readings_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.iot_readings
    ADD CONSTRAINT iot_readings_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.gate_devices(id);


--
-- Name: ticket_manifest ticket_manifest_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_manifest
    ADD CONSTRAINT ticket_manifest_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.edge_events(id);


--
-- PostgreSQL database dump complete
--

\unrestrict ubejBz94KABZhF00llylYTUyvTW9XRcMbkUdByQMjrXw6jGYWjUUjUckP59nKFv

