-- One row per integration that has been set up, holding its settings as JSON. It's apart from the
-- settings document, which the General page saves whole. Secrets live in the keychain.
CREATE TABLE integrations (
    id TEXT PRIMARY KEY,
    body TEXT NOT NULL
);
