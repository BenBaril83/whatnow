-- Chat messages are keyed by date, not day, so chat works before the day is started.
CREATE TABLE messages (
    id INTEGER PRIMARY KEY,
    date TEXT NOT NULL,
    role TEXT NOT NULL,
    text TEXT NOT NULL,
    tools TEXT NOT NULL DEFAULT '[]',
    batch_id INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX messages_date ON messages(date, id);

-- The running summary of a date's chat. Messages up to through_id are folded into it.
CREATE TABLE chat_folds (
    date TEXT PRIMARY KEY,
    summary TEXT NOT NULL,
    through_id INTEGER NOT NULL
);
