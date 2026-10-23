CREATE TABLE processes (
    id TEXT PRIMARY KEY NOT NULL,
    state TEXT NOT NULL,
    process_definition_id TEXT NOT NULL,
    current_steps TEXT NOT NULL CHECK (json_valid(current_steps)),
    inputs TEXT NOT NULL CHECK (json_valid(inputs)),
    result TEXT NOT NULL CHECK (json_valid(result)),
    executions INTEGER NOT NULL,
    execution_time INTEGER NOT NULL,
    executed_at DATETIME,
    version INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX processes_state_executed_at_idx ON processes (state, executed_at);

CREATE TABLE process_steps (
    id TEXT PRIMARY KEY NOT NULL,
    state TEXT NOT NULL,
    process_id TEXT NOT NULL REFERENCES processes(id),
    process_step_definition_name TEXT NOT NULL,
    type TEXT NOT NULL,
    params TEXT NOT NULL CHECK (json_valid(params)),
    result TEXT NOT NULL CHECK (json_valid(result)),
    reliability TEXT NOT NULL CHECK (json_valid(reliability)),
    execution_policy TEXT NOT NULL CHECK (json_valid(execution_policy)),
    version INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    executed_at DATETIME,
    UNIQUE (process_id, process_step_definition_name)
);
CREATE INDEX process_steps_type_state ON process_steps (type, state);
CREATE INDEX process_steps_state_executed_at_idx ON process_steps (state, executed_at);
