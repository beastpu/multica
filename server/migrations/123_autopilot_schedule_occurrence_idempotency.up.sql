ALTER TABLE autopilot_run
    ADD COLUMN IF NOT EXISTS scheduled_fire_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS idx_autopilot_run_schedule_occurrence
    ON autopilot_run (trigger_id, source, scheduled_fire_at)
    WHERE source = 'schedule'
      AND trigger_id IS NOT NULL
      AND scheduled_fire_at IS NOT NULL;
