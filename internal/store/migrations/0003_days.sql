CREATE TABLE days (
    id INTEGER PRIMARY KEY,
    date TEXT NOT NULL UNIQUE,
    pace INTEGER NOT NULL,
    mood_note TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    wrapped_at TEXT
);

-- Plans are versioned history, not activity: a replan adds a version and never edits one.
CREATE TABLE plans (
    id INTEGER PRIMARY KEY,
    day_id INTEGER NOT NULL REFERENCES days(id),
    version INTEGER NOT NULL,
    triggered_by TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    opening TEXT NOT NULL,
    entries TEXT NOT NULL,
    fallback INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (day_id, version)
);
