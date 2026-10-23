ALTER TABLE process_steps ADD COLUMN context TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(context));
