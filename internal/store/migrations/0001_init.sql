CREATE TABLE tasks (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    raw_text TEXT NOT NULL DEFAULT '',
    size TEXT,
    energy TEXT,
    feeling TEXT NOT NULL DEFAULT '',
    due_on TEXT,
    not_before TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    refs TEXT NOT NULL DEFAULT '[]',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE blockers (
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    blocked_by_id INTEGER NOT NULL REFERENCES tasks(id),
    PRIMARY KEY (task_id, blocked_by_id)
);

CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    starts_at TEXT NOT NULL,
    ends_at TEXT,
    raw_text TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);
CREATE INDEX events_starts_at ON events(starts_at);

CREATE TABLE batches (
    id INTEGER PRIMARY KEY,
    actor TEXT NOT NULL,
    created_at TEXT NOT NULL,
    undone_at TEXT
);

CREATE TABLE activity (
    id INTEGER PRIMARY KEY,
    batch_id INTEGER NOT NULL REFERENCES batches(id),
    action TEXT NOT NULL,
    entity TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    before_json TEXT,
    after_json TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX activity_batch ON activity(batch_id);
CREATE INDEX activity_entity ON activity(entity, entity_id);
