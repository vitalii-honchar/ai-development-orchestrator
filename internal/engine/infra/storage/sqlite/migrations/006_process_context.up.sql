ALTER TABLE processes ADD COLUMN context BLOB CHECK (context IS NULL OR json_valid(context, 8));

UPDATE processes SET context = jsonb(json_object(
    'inputs', json(inputs),
    'step_outputs', json((
        SELECT json_group_object(s.process_step_definition_name, json(s.result))
        FROM process_steps s
        WHERE s.process_id = processes.id AND s.state = 'COMPLETED'
    ))
));

ALTER TABLE processes DROP COLUMN inputs;
