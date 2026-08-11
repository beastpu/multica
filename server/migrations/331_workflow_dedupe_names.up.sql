-- Uniqueness stops exempting anyone, so names that were only distinct under the
-- old rule have to be separated first. Under archiving, "retire the old one and
-- recreate it under the same name" was the normal revision path, so a workspace
-- can hold several workflows called the same thing — the test environment has
-- four named 缺陷修复.
--
-- Same treatment migration 309 gave live duplicates: the newest keeps the name
-- because it is the one people have been using, older siblings take a numbered
-- suffix. Renaming is recoverable by hand; deleting them here would not be, and
-- deciding which history to destroy is not a migration's call.
WITH ranked AS (
    SELECT id,
           name,
           row_number() OVER (
               PARTITION BY workspace_id, lower(btrim(name))
               ORDER BY updated_at DESC, id DESC
           ) AS position
    FROM workflow
)
UPDATE workflow AS target
SET name = ranked.name || ' (' || ranked.position || ')',
    updated_at = now()
FROM ranked
WHERE target.id = ranked.id
  AND ranked.position > 1;
