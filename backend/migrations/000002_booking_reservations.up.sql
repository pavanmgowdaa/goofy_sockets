-- Additive online-booking reservation expiry. Existing rows remain unchanged.
ALTER TABLE bookings ADD COLUMN expires_at timestamptz;
CREATE INDEX bookings_pending_expiry_idx
    ON bookings (expires_at)
    WHERE status = 'pending' AND expires_at IS NOT NULL;
