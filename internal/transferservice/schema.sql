CREATE TABLE IF NOT EXISTS transfers (
    transfer_id TEXT PRIMARY KEY,
    visitor_id TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    status TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE transfers ADD COLUMN IF NOT EXISTS rejection_reason TEXT;
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS trace_id TEXT;
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS scenario TEXT NOT NULL DEFAULT 'happy_path';

CREATE INDEX IF NOT EXISTS transfers_by_visitor ON transfers (visitor_id, requested_at);

CREATE TABLE IF NOT EXISTS transfer_history (
    entry_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transfer_id TEXT NOT NULL REFERENCES transfers (transfer_id),
    step TEXT NOT NULL,
    service TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    message_id TEXT,
    causation_id TEXT,
    issued_message_id TEXT
);

ALTER TABLE transfer_history ALTER COLUMN step DROP NOT NULL;
ALTER TABLE transfer_history ADD COLUMN IF NOT EXISTS observation TEXT CHECK (num_nonnulls(step, observation) = 1);
ALTER TABLE transfer_history ADD COLUMN IF NOT EXISTS attempt_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS transfer_history_by_message ON transfer_history (message_id);

CREATE INDEX IF NOT EXISTS transfer_history_by_transfer ON transfer_history (transfer_id, entry_id);

CREATE UNIQUE INDEX IF NOT EXISTS one_pending_transfer_per_visitor ON transfers (visitor_id)
    WHERE status IN ('debit_pending', 'credit_pending');
