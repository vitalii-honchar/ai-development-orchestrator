CREATE TABLE users (
    username TEXT PRIMARY KEY NOT NULL,
    password TEXT NOT NULL,
    state TEXT NOT NULL,
    role TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
