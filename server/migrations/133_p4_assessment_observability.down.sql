ALTER TABLE agent_fix_p4_assessment
    DROP COLUMN IF EXISTS assessment_issue_id,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS last_error;
