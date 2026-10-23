CREATE TABLE push_keys (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    last_used_at TEXT
);
