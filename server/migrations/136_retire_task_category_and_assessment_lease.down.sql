ALTER TABLE agent_task_queue
    ADD COLUMN task_category TEXT NOT NULL DEFAULT 'fix'
        CHECK (task_category IN ('fix', 'analysis'));

-- Backfill the same way migration 130 did: assessment tasks are identified by
-- their context type, everything else keeps the 'fix' default.
UPDATE agent_task_queue
    SET task_category = 'analysis'
    WHERE context->>'type' = 'agent_fix_p4_assessment';

ALTER TABLE agent_fix_p4_assessment
    ADD COLUMN leased_until timestamptz;
