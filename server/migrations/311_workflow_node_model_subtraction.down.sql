ALTER TABLE workflow_acceptance
    ALTER COLUMN workflow_node_instance_id SET NOT NULL;

ALTER TABLE workflow_node_participant
    DROP CONSTRAINT workflow_node_participant_role_check;

ALTER TABLE workflow_node_participant
    ADD CONSTRAINT workflow_node_participant_role_check
    CHECK (role IN ('owner', 'participant', 'approver'));

ALTER TABLE workflow_node_instance
    DROP CONSTRAINT workflow_node_instance_status_check;

ALTER TABLE workflow_node_instance
    ADD CONSTRAINT workflow_node_instance_status_check
    CHECK (status IN (
        'pending',
        'ready',
        'active',
        'waiting',
        'completed',
        'blocked',
        'failed',
        'skipped',
        'superseded',
        'cancelled'
    ));

-- Recreated with the indexes 221/236 built and the primary key 239 attached,
-- inline rather than CONCURRENTLY: the table is empty on the way back.
CREATE TABLE workflow_node_confirmation (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    member_id UUID NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    comment TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_confirmation_member
ON workflow_node_confirmation (workflow_node_instance_id, member_id);
