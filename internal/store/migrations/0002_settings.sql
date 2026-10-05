-- One row holding the settings document as JSON. New settings fields need no migration.
CREATE TABLE settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    body TEXT NOT NULL
);
