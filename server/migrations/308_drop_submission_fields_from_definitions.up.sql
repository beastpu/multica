-- Strip the retired submission_schema.fields from every stored definition.
--
-- Definitions are decoded with DisallowUnknownFields, so a stored document
-- carrying a key the struct no longer has fails to parse outright — an existing
-- template stops being usable rather than degrading. Removing the key from the
-- data is what makes the schema change complete; leaving it would require the
-- Go type to keep a field it no longer honours purely so old rows still decode.
--
-- Template versions hold a whole definition (fields sit under each node);
-- node and task snapshots hold a single node (fields sit at the top level).

UPDATE workflow_template_version
SET definition = jsonb_set(
        definition,
        '{nodes}',
        (
            SELECT jsonb_agg(
                CASE
                    WHEN node ? 'submission_schema'
                        THEN jsonb_set(
                            node,
                            '{submission_schema}',
                            (node -> 'submission_schema') - 'fields'
                        )
                    ELSE node
                END
                ORDER BY ordinality
            )
            FROM jsonb_array_elements(definition -> 'nodes')
                 WITH ORDINALITY AS elements(node, ordinality)
        )
    )
WHERE jsonb_typeof(definition -> 'nodes') = 'array'
  AND definition::text LIKE '%"fields"%';

UPDATE workflow_node_instance
SET definition_snapshot = jsonb_set(
        definition_snapshot,
        '{submission_schema}',
        (definition_snapshot -> 'submission_schema') - 'fields'
    )
WHERE definition_snapshot ? 'submission_schema'
  AND (definition_snapshot -> 'submission_schema') ? 'fields';

UPDATE workflow_node_task
SET definition_snapshot = jsonb_set(
        definition_snapshot,
        '{submission_schema}',
        (definition_snapshot -> 'submission_schema') - 'fields'
    )
WHERE definition_snapshot ? 'submission_schema'
  AND (definition_snapshot -> 'submission_schema') ? 'fields';
