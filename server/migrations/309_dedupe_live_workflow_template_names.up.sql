-- Live templates sharing a name become indistinguishable in the picker, so the
-- next migration makes the name unique. Existing duplicates have to go first,
-- or that index cannot be built.
--
-- The newest row keeps the name (it is the one people have been using); older
-- siblings get a numbered suffix rather than being deleted or archived —
-- renaming is recoverable by hand, and the other two are not.
--
-- Archived rows are left alone: they are excluded from the constraint because
-- "archive the old one, recreate under the same name" is the normal revision
-- path once runs depend on the old version.
WITH ranked AS (
    SELECT id,
           name,
           row_number() OVER (
               PARTITION BY workspace_id, lower(btrim(name))
               ORDER BY updated_at DESC, id DESC
           ) AS position
    FROM workflow_template
    WHERE status <> 'archived'
)
UPDATE workflow_template AS t
SET name = ranked.name || ' (' || ranked.position || ')'
FROM ranked
WHERE t.id = ranked.id
  AND ranked.position > 1;
