DROP INDEX IF EXISTS idx_autopilot_run_schedule_occurrence;

ALTER TABLE autopilot_run
    DROP COLUMN IF EXISTS scheduled_fire_at;
