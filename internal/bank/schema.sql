CREATE TABLE IF NOT EXISTS accounts (
    visitor_id TEXT PRIMARY KEY,
    balance BIGINT NOT NULL CHECK (balance >= 0)
);
