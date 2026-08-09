-- handoff_required was retired from the workflow definition schema. Whether a
-- node owes the next one its conclusion is engine behaviour, not a per-node
-- switch a template author configures, and every activity now owes one.
-- Strip it from stored definitions so runtime parsing can remain strict.
UPDATE workflow_version
SET definition = jsonb_set(
        definition,
        '{nodes}',
        (
            SELECT jsonb_agg(
                CASE
                    WHEN node->'completion' ? 'handoff_required'
                        THEN jsonb_set(
                            node,
                            '{completion}',
                            (node->'completion') - 'handoff_required'
                        )
                    ELSE node
                END
                ORDER BY ordinality
            )
            FROM jsonb_array_elements(definition->'nodes')
                WITH ORDINALITY AS element(node, ordinality)
        )
    ),
    definition_checksum = ''
WHERE jsonb_typeof(definition->'nodes') = 'array'
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements(definition->'nodes') AS element(node)
    WHERE node->'completion' ? 'handoff_required'
  );

-- Node snapshots are decoded leniently, so a leftover key would not fail a
-- running instance today. It is removed anyway: a snapshot that still carries
-- a retired switch reads as if the switch still means something.
UPDATE workflow_node_instance
SET definition_snapshot = jsonb_set(
        definition_snapshot,
        '{completion}',
        (definition_snapshot->'completion') - 'handoff_required'
    )
WHERE definition_snapshot->'completion' ? 'handoff_required';
