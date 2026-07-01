CREATE TABLE IF NOT EXISTS agent_fix_p4_assessment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    feishu_binding_id UUID NOT NULL REFERENCES feishu_project_issue_binding(id) ON DELETE CASCADE,
    assessment_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    assessment_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (assessment_status IN ('pending', 'running', 'completed', 'failed', 'stale')),
    delivery_attribution_prediction TEXT NOT NULL DEFAULT 'unknown'
        CHECK (delivery_attribution_prediction IN ('ai_delivered', 'ai_assisted', 'human_delivered', 'conflict', 'unattributed', 'unknown')),
    quality_prediction TEXT NOT NULL DEFAULT 'unknown'
        CHECK (quality_prediction IN ('likely_correct', 'likely_needs_changes', 'likely_wrong', 'unknown')),
    prediction_reasons TEXT[] NOT NULL DEFAULT '{}',
    confidence NUMERIC(4,3)
        CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    workstream TEXT NOT NULL DEFAULT '',
    swarm_reviews JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(swarm_reviews) = 'array'),
    ai_shelved_cls INTEGER[] NOT NULL DEFAULT '{}',
    swarm_change_cls INTEGER[] NOT NULL DEFAULT '{}',
    swarm_committed_cls INTEGER[] NOT NULL DEFAULT '{}',
    external_committed_cls INTEGER[] NOT NULL DEFAULT '{}',
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(evidence) = 'object'),
    summary TEXT NOT NULL DEFAULT '',
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(warnings) = 'array'),
    model TEXT,
    prompt_version TEXT NOT NULL DEFAULT 'p4-assessment-v1',
    assessed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, feishu_binding_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_fix_p4_assessment_issue
    ON agent_fix_p4_assessment(issue_id);
CREATE INDEX IF NOT EXISTS idx_agent_fix_p4_assessment_workspace_updated
    ON agent_fix_p4_assessment(workspace_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS agent_fix_review (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    feishu_binding_id UUID NOT NULL REFERENCES feishu_project_issue_binding(id) ON DELETE CASCADE,
    p4_assessment_id UUID REFERENCES agent_fix_p4_assessment(id) ON DELETE SET NULL,
    outcome TEXT NOT NULL DEFAULT 'unreviewed'
        CHECK (outcome IN ('unreviewed', 'accepted', 'needs_changes', 'rejected', 'not_applicable')),
    reasons TEXT[] NOT NULL DEFAULT '{}',
    note TEXT NOT NULL DEFAULT '',
    reviewer_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, feishu_binding_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_fix_review_issue
    ON agent_fix_review(issue_id);
CREATE INDEX IF NOT EXISTS idx_agent_fix_review_workspace_updated
    ON agent_fix_review(workspace_id, updated_at DESC);
