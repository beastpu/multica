-- One live template per name per workspace. Partial so archived rows are
-- exempt: replacing a template by archiving the old one and recreating it
-- under the same name has to stay possible, because runs pin the old version
-- and editing it in place is not an option.
--
-- lower(btrim(...)) so casing and padding cannot buy a second copy — they are
-- invisible to whoever reads the list.
--
-- The handler checks this too and returns 409 with a message naming the field;
-- this index is the backstop for the race between check and insert.
CREATE UNIQUE INDEX CONCURRENTLY workflow_template_live_name_idx
    ON workflow_template (workspace_id, lower(btrim(name)))
    WHERE status <> 'archived';
