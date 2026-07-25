ALTER TABLE workflow_executor_resolution
    DROP CONSTRAINT workflow_executor_resolution_strategy_check;

ALTER TABLE workflow_executor_resolution
    ADD CONSTRAINT workflow_executor_resolution_strategy_check
    CHECK (strategy IN (
        'fixed_actor',
        'fixed_role',
        'previous_selected',
        'capability_match',
        'fallback_role',
        'manual'
    ));
