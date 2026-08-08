-- Rename this branch's workflow migration records to their renumbered files.
--
-- The workflow engine and several develop features both numbered from 201, so
-- thirty prefixes ended up shared. Duplicate prefixes have no defined order
-- between them: which of two `202_` files runs first depends on how the runner
-- sorts, and a later `202_a...` would jump ahead of both. The lint that catches
-- this freezes the historical duplicates up to 148 and requires uniqueness
-- after, so the workflow set moved to a contiguous block above every existing
-- prefix.
--
-- Renaming files orphans the rows that recorded them: schema_migrations stores
-- the full stem, so every renamed migration reads as unapplied and would be
-- replayed. `263_workflow_domain` creates thirteen tables without IF NOT
-- EXISTS, so that replay fails and takes the deploy with it.
--
-- Doing the rename here rather than by hand means every database repairs
-- itself, in the same order, with a record of why. On a fresh database these
-- statements match nothing and cost one no-op pass.
--
-- This repairs once. A migration is recorded as applied whether or not its
-- statements matched anything, so a database that reaches this point already
-- holding some other spelling of these versions will not be repaired by a
-- second run. Every database that exists today holds the pre-rename names, so
-- the single pass is enough; a database in some third state needs the mapping
-- applied by hand, and the pairs below are that mapping.

UPDATE schema_migrations SET version = '263_workflow_domain' WHERE version = '202_workflow_domain';
UPDATE schema_migrations SET version = '264_workflow_template_workspace_status_index' WHERE version = '203_workflow_template_workspace_status_index';
UPDATE schema_migrations SET version = '265_workflow_template_version_unique_index' WHERE version = '204_workflow_template_version_unique_index';
UPDATE schema_migrations SET version = '266_workflow_template_one_draft_index' WHERE version = '205_workflow_template_one_draft_index';
UPDATE schema_migrations SET version = '267_workflow_instance_active_host_index' WHERE version = '206_workflow_instance_active_host_index';
UPDATE schema_migrations SET version = '268_workflow_instance_workspace_status_index' WHERE version = '207_workflow_instance_workspace_status_index';
UPDATE schema_migrations SET version = '269_workflow_node_instance_attempt_index' WHERE version = '208_workflow_node_instance_attempt_index';
UPDATE schema_migrations SET version = '270_workflow_node_instance_status_index' WHERE version = '209_workflow_node_instance_status_index';
UPDATE schema_migrations SET version = '271_workflow_node_task_key_index' WHERE version = '210_workflow_node_task_key_index';
UPDATE schema_migrations SET version = '272_workflow_node_task_materialization_index' WHERE version = '211_workflow_node_task_materialization_index';
UPDATE schema_migrations SET version = '273_workflow_node_submission_revision_index' WHERE version = '212_workflow_node_submission_revision_index';
UPDATE schema_migrations SET version = '274_workflow_node_verdict_revision_index' WHERE version = '213_workflow_node_verdict_revision_index';
UPDATE schema_migrations SET version = '275_workflow_acceptance_revision_index' WHERE version = '214_workflow_acceptance_revision_index';
UPDATE schema_migrations SET version = '276_workflow_event_idempotency_index' WHERE version = '215_workflow_event_idempotency_index';
UPDATE schema_migrations SET version = '277_workflow_event_instance_created_index' WHERE version = '216_workflow_event_instance_created_index';
UPDATE schema_migrations SET version = '278_issue_workflow_origin_index' WHERE version = '217_issue_workflow_origin_index';
UPDATE schema_migrations SET version = '279_workflow_role_assignment_unique_index' WHERE version = '218_workflow_role_assignment_unique_index';
UPDATE schema_migrations SET version = '280_workflow_template_version_revision' WHERE version = '219_workflow_template_version_revision';
UPDATE schema_migrations SET version = '281_workflow_start_idempotency_index' WHERE version = '220_workflow_start_idempotency_index';
UPDATE schema_migrations SET version = '282_workflow_confirmation_member_index' WHERE version = '221_workflow_confirmation_member_index';
UPDATE schema_migrations SET version = '283_workflow_node_task_issue_index' WHERE version = '222_workflow_node_task_issue_index';
UPDATE schema_migrations SET version = '284_workflow_node_participant_node_index' WHERE version = '223_workflow_node_participant_node_index';
UPDATE schema_migrations SET version = '285_workflow_executor_resolution_lookup_index' WHERE version = '224_workflow_executor_resolution_lookup_index';
UPDATE schema_migrations SET version = '286_workflow_acceptance_idempotency_index' WHERE version = '225_workflow_acceptance_idempotency_index';
UPDATE schema_migrations SET version = '287_workflow_template_primary_index' WHERE version = '226_workflow_template_primary_index';
UPDATE schema_migrations SET version = '288_workflow_template_version_primary_index' WHERE version = '227_workflow_template_version_primary_index';
UPDATE schema_migrations SET version = '289_workflow_instance_primary_index' WHERE version = '228_workflow_instance_primary_index';
UPDATE schema_migrations SET version = '290_workflow_role_assignment_primary_index' WHERE version = '229_workflow_role_assignment_primary_index';
UPDATE schema_migrations SET version = '291_workflow_node_instance_primary_index' WHERE version = '230_workflow_node_instance_primary_index';
UPDATE schema_migrations SET version = '292_workflow_node_participant_primary_index' WHERE version = '231_workflow_node_participant_primary_index';
UPDATE schema_migrations SET version = '293_workflow_executor_resolution_primary_index' WHERE version = '232_workflow_executor_resolution_primary_index';
UPDATE schema_migrations SET version = '294_workflow_node_task_primary_index' WHERE version = '233_workflow_node_task_primary_index';
UPDATE schema_migrations SET version = '295_workflow_node_submission_primary_index' WHERE version = '234_workflow_node_submission_primary_index';
UPDATE schema_migrations SET version = '296_workflow_node_verdict_primary_index' WHERE version = '235_workflow_node_verdict_primary_index';
UPDATE schema_migrations SET version = '297_workflow_node_confirmation_primary_index' WHERE version = '236_workflow_node_confirmation_primary_index';
UPDATE schema_migrations SET version = '298_workflow_acceptance_primary_index' WHERE version = '237_workflow_acceptance_primary_index';
UPDATE schema_migrations SET version = '299_workflow_event_primary_index' WHERE version = '238_workflow_event_primary_index';
UPDATE schema_migrations SET version = '300_workflow_primary_constraints' WHERE version = '239_workflow_primary_constraints';
UPDATE schema_migrations SET version = '301_workflow_instance_reconcile_after' WHERE version = '240_workflow_instance_reconcile_after';
UPDATE schema_migrations SET version = '302_workflow_direct_executor_strategy' WHERE version = '241_workflow_direct_executor_strategy';
UPDATE schema_migrations SET version = '303_workflow_artifact' WHERE version = '242_workflow_artifact';
UPDATE schema_migrations SET version = '304_workflow_artifact_primary_index' WHERE version = '243_workflow_artifact_primary_index';
UPDATE schema_migrations SET version = '305_workflow_artifact_current_index' WHERE version = '244_workflow_artifact_current_index';
UPDATE schema_migrations SET version = '306_workflow_artifact_instance_index' WHERE version = '245_workflow_artifact_instance_index';
UPDATE schema_migrations SET version = '307_workflow_submission_choice' WHERE version = '246_workflow_submission_choice';
UPDATE schema_migrations SET version = '308_drop_submission_fields_from_definitions' WHERE version = '247_drop_submission_fields_from_definitions';
UPDATE schema_migrations SET version = '309_dedupe_live_workflow_template_names' WHERE version = '248_dedupe_live_workflow_template_names';
UPDATE schema_migrations SET version = '310_workflow_template_name_unique' WHERE version = '249_workflow_template_name_unique';
UPDATE schema_migrations SET version = '311_workflow_node_model_subtraction' WHERE version = '250_workflow_node_model_subtraction';
UPDATE schema_migrations SET version = '312_workflow_standalone_runs' WHERE version = '251_workflow_standalone_runs';
UPDATE schema_migrations SET version = '313_workflow_direct_agent_tasks' WHERE version = '252_workflow_direct_agent_tasks';
UPDATE schema_migrations SET version = '314_agent_task_workflow_node_task_index' WHERE version = '253_agent_task_workflow_node_task_index';
UPDATE schema_migrations SET version = '315_workflow_definition_rename' WHERE version = '254_workflow_definition_rename';
UPDATE schema_migrations SET version = '316_workflow_critic_task_source' WHERE version = '255_workflow_critic_task_source';
UPDATE schema_migrations SET version = '317_workflow_drop_draft_state' WHERE version = '256_workflow_drop_draft_state';
UPDATE schema_migrations SET version = '318_drop_workflow_definition_applies_to' WHERE version = '257_drop_workflow_definition_applies_to';
UPDATE schema_migrations SET version = '319_workflow_drop_submission_choice' WHERE version = '258_workflow_drop_submission_choice';
