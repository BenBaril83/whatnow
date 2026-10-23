-- Hosted mode keeps secrets here, sealed with a key from the environment. Local databases get the
-- table too and never use it; their secrets live in the keychain.
CREATE TABLE secrets (
    name TEXT PRIMARY KEY,
    sealed BLOB NOT NULL
);
