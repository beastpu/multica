-- Archiving is retired. It existed because a workflow with runs could not be
-- deleted, so retiring one meant hiding it; now that deletion is allowed once
-- every run has finished, the hidden state has no job left. It was also a
-- one-way door — nothing ever restored a workflow — which made it the wrong
-- tool for "I am done with this process".
--
-- Archived workflows become ordinary ones, visible for exactly as long as it
-- takes their owner to delete them, which is the action they actually wanted.
-- There is no UPDATE to 'published' first: the column is going away, so its
-- value stops meaning anything. Setting it would also have collided with the
-- partial unique index below, which exempted archived rows — three archived
-- workflows sharing a live one's name is normal data under the old rule.
--
-- With one value left the column says nothing, the same way
-- workflow_version.status did before migration 317 dropped it. The API keeps
-- no field for it: the client schema defaults status to "published" and
-- archived_at to null, so an older desktop reads the new shape correctly.
--
-- Dropping the column takes its dependent indexes with it, silently:
-- workflow_live_name_idx (partial, over status) and
-- idx_workflow_workspace_status. Migrations 331 and 332 rebuild both.
ALTER TABLE workflow
    DROP CONSTRAINT workflow_template_status_check;

ALTER TABLE workflow
    DROP COLUMN status;

ALTER TABLE workflow
    DROP COLUMN archived_at;
