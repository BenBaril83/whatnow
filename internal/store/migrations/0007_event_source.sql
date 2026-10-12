-- An event pulled from an integration names it, and its identity there. A deleted row that keeps
-- its key is the tombstone that stops the next pull bringing the event back.
ALTER TABLE events ADD COLUMN source TEXT;
ALTER TABLE events ADD COLUMN source_key TEXT;
CREATE UNIQUE INDEX events_source_key ON events(source, source_key);
