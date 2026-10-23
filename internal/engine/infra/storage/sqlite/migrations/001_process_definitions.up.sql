CREATE TABLE process_definitions (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    state TEXT NOT NULL,
    version INTEGER NOT NULL,
    graph TEXT NOT NULL CHECK (json_valid(graph)),
    reliability TEXT NOT NULL CHECK (json_valid(reliability)),
    inputs TEXT NOT NULL CHECK (json_valid(inputs)),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX process_definitions_name_state_unique
    ON process_definitions (name, state);
