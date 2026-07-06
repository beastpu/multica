import { describe, expect, it } from "vitest";
import {
  AgentFixHumanReviewSchema,
  AgentFixRecordListSchema,
  AppConfigSchema,
  DashboardAgentRunTimeListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  CreateFeedbackResponseSchema,
  DuplicateIssueErrorBodySchema,
  EMPTY_FEISHU_PROJECT_INTEGRATION,
  EMPTY_CREATE_FEEDBACK_RESPONSE,
  EMPTY_INBOX_UNREAD_SUMMARY,
  EMPTY_USER,
  FeishuProjectIntegrationSchema,
  InboxUnreadSummarySchema,
  IssueTriggerPreviewSchema,
  ListIssuesResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  SquadListSchema,
  SquadSchema,
  TimelineEntriesSchema,
  TriggerAgentFixP4AssessmentResponseSchema,
  UserSchema,
  WorkspaceCapabilitySchema,
  EMPTY_WORKSPACE_CAPABILITY,
} from "./schemas";
import { parseWithFallback } from "./schema";

const baseIssue = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  number: 1,
  identifier: "MUL-1",
  title: "Test",
  description: null,
  status: "todo",
  priority: "medium",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  stage: null,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("IssueSchema (via ListIssuesResponseSchema)", () => {
  it("accepts a primitive metadata KV map", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: { pipeline_status: "waiting", pr_number: 3, is_blocked: true },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({
      pipeline_status: "waiting",
      pr_number: 3,
      is_blocked: true,
    });
  });

  it("drops object-valued reserved keys (agent_work) instead of failing the issue", () => {
    // Derived assessment issues carry metadata.agent_work as a nested OBJECT.
    // A strict primitive-only record used to fail the whole IssueSchema here,
    // blanking the issue page into the empty fallback.
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: {
            flow_cl: "12345",
            agent_work: { kind: "p4_assessment", trigger: "scan" },
          },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({ flow_cl: "12345" });
    expect(parsed.issues[0]?.title).toBe(baseIssue.title);
  });

  it("defaults metadata to {} when the server omits it (older backend)", () => {
    const { metadata: _omit, ...issueWithoutMetadata } = baseIssue;
    const payload = { issues: [issueWithoutMetadata], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({});
  });

  it("accepts and defaults Feishu Project external fields separately from metadata", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          external_fields: { "提交分支": "1.7.2(dev or rel)", "开发分支": "main, rel_1.1.0" },
        },
        baseIssue,
      ],
      total: 2,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.external_fields).toEqual({
      "提交分支": "1.7.2(dev or rel)",
      "开发分支": "main, rel_1.1.0",
    });
    expect(parsed.issues[1]?.external_fields).toEqual({});
  });

  it("strips non-primitive metadata values instead of rejecting the issue", () => {
    // Rejecting used to blank the whole issue page (the agent_work incident);
    // the schema now degrades by dropping the offending entry.
    const payload = {
      issues: [{ ...baseIssue, metadata: { nested: { x: 1 }, keep: "v" } }],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({ keep: "v" });
  });

  it("accepts a numeric stage", () => {
    const payload = { issues: [{ ...baseIssue, stage: 2 }], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.stage).toBe(2);
  });

  it("defaults stage to null when the server omits it (older backend)", () => {
    const { stage: _omit, ...issueWithoutStage } = baseIssue;
    const payload = { issues: [issueWithoutStage], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.stage).toBeNull();
  });
});

// POST /api/issues/preview-trigger feeds this schema through parseWithFallback
// in client.previewIssueTrigger with fallback { triggers: [], total_count: 0 }
// (MUL-3375). The four entry points read it to decide "will this start a run",
// so malformed / missing / null drift must degrade to "nothing will start"
// rather than throw into the picker/modal.
const PREVIEW_FALLBACK = { triggers: [], total_count: 0 };
const PREVIEW_ENDPOINT = { endpoint: "POST /api/issues/preview-trigger" };

describe("IssueTriggerPreviewSchema", () => {
  it("parses a well-formed response", () => {
    const parsed = IssueTriggerPreviewSchema.parse({
      triggers: [
        { issue_id: "i1", agent_id: "a1", source: "assign", handoff_supported: true },
        { issue_id: "i2", agent_id: "a2", source: "status", handoff_supported: false },
      ],
      total_count: 2,
    });
    expect(parsed.total_count).toBe(2);
    expect(parsed.triggers).toHaveLength(2);
    expect(parsed.triggers[0]).toMatchObject({ issue_id: "i1", agent_id: "a1", source: "assign", handoff_supported: true });
  });

  it("defaults missing top-level fields (empty / older backend)", () => {
    const parsed = IssueTriggerPreviewSchema.parse({});
    expect(parsed.triggers).toEqual([]);
    expect(parsed.total_count).toBe(0);
  });

  it("defaults missing optional item fields, keeping required issue_id", () => {
    const parsed = IssueTriggerPreviewSchema.parse({ triggers: [{ issue_id: "i1" }], total_count: 1 });
    expect(parsed.triggers[0]).toEqual({
      issue_id: "i1",
      agent_id: "",
      source: "",
      handoff_supported: false,
    });
  });

  it("parseWithFallback returns the fallback for a malformed shape (triggers not an array)", () => {
    const parsed = parseWithFallback(
      { triggers: "nope", total_count: 1 },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback when an item drops the required issue_id", () => {
    const parsed = parseWithFallback(
      { triggers: [{ agent_id: "a1", source: "assign" }], total_count: 1 },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback for a wrong-typed total_count", () => {
    const parsed = parseWithFallback(
      { triggers: [], total_count: "5" },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback for null / non-object bodies", () => {
    expect(parseWithFallback(null, IssueTriggerPreviewSchema, PREVIEW_FALLBACK, PREVIEW_ENDPOINT)).toEqual(PREVIEW_FALLBACK);
    expect(parseWithFallback("oops", IssueTriggerPreviewSchema, PREVIEW_FALLBACK, PREVIEW_ENDPOINT)).toEqual(PREVIEW_FALLBACK);
  });
});

describe("TimelineEntriesSchema", () => {
  it("preserves source_task_id for agent failure comments", () => {
    const parsed = TimelineEntriesSchema.parse([
      {
        type: "comment",
        id: "comment-1",
        actor_type: "agent",
        actor_id: "agent-1",
        created_at: "2026-01-01T00:00:00Z",
        content: "API Error: 500 Internal server error",
        comment_type: "system",
        source_task_id: "task-1",
      },
    ]);

    expect(parsed[0]?.source_task_id).toBe("task-1");
  });
});

describe("CreateFeedbackResponseSchema", () => {
  const ENDPOINT = { endpoint: "POST /api/feedback" };

  it("parses a well-formed response and preserves extra fields", () => {
    const parsed = parseWithFallback(
      { id: "feedback-1", created_at: "2026-06-26T00:00:00Z", future_field: true },
      CreateFeedbackResponseSchema,
      EMPTY_CREATE_FEEDBACK_RESPONSE,
      ENDPOINT,
    );
    expect(parsed).toMatchObject({
      id: "feedback-1",
      created_at: "2026-06-26T00:00:00Z",
      future_field: true,
    });
  });

  it("returns the empty fallback for malformed feedback responses", () => {
    expect(
      parseWithFallback(
        { id: 123, created_at: "2026-06-26T00:00:00Z" },
        CreateFeedbackResponseSchema,
        EMPTY_CREATE_FEEDBACK_RESPONSE,
        ENDPOINT,
      ),
    ).toBe(EMPTY_CREATE_FEEDBACK_RESPONSE);
    expect(
      parseWithFallback(null, CreateFeedbackResponseSchema, EMPTY_CREATE_FEEDBACK_RESPONSE, ENDPOINT),
    ).toBe(EMPTY_CREATE_FEEDBACK_RESPONSE);
  });
});

// The duplicate-issue branch in create-issue.tsx feeds ApiError.body
// (typed as `unknown`) through this schema. Any future server drift that
// loses the contract MUST fail the parse so the UI falls back to a normal
// error toast instead of rendering an empty / partial duplicate card.
describe("DuplicateIssueErrorBodySchema", () => {
  const valid = {
    code: "active_duplicate_issue",
    error: "An active issue with this title already exists: MUL-12 – Login bug",
    issue: {
      id: "11111111-1111-1111-1111-111111111111",
      identifier: "MUL-12",
      title: "Login bug",
    },
  };

  it("accepts a well-formed body", () => {
    expect(DuplicateIssueErrorBodySchema.safeParse(valid).success).toBe(true);
  });

  it("accepts unknown extra fields via .loose()", () => {
    const forwardCompat = {
      ...valid,
      hint: "Try a different title",
      issue: { ...valid.issue, workspace_id: "ws-1", status: "todo" },
    };
    expect(DuplicateIssueErrorBodySchema.safeParse(forwardCompat).success).toBe(true);
  });

  it("rejects a renamed code (so renames degrade to the generic toast)", () => {
    const renamed = { ...valid, code: "duplicate_issue" };
    expect(DuplicateIssueErrorBodySchema.safeParse(renamed).success).toBe(false);
  });

  it("rejects a missing issue object", () => {
    const { issue: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(false);
  });

  it("rejects a non-string issue.id", () => {
    const broken = { ...valid, issue: { ...valid.issue, id: 42 } };
    expect(DuplicateIssueErrorBodySchema.safeParse(broken).success).toBe(false);
  });

  it("accepts a missing error field (it is optional)", () => {
    const { error: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(true);
  });
});

// `user.timezone` (Viewing tz) was added in the timezone-architecture RFC.
// A desktop build older than the server — or a server predating the
// `user.timezone` migration — will return a `/api/me` body with no
// `timezone` key. The schema must not fail closed on that: the field
// defaults to `null`, which the frontend resolves to the browser-detected
// tz at render time.
describe("UserSchema timezone drift", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    name: "Ada",
    email: "ada@example.com",
  };

  it("defaults timezone to null when the field is absent", () => {
    const parsed = UserSchema.parse(base);
    expect(parsed.timezone).toBe(null);
  });

  it("preserves an explicit IANA timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: "Asia/Tokyo" });
    expect(parsed.timezone).toBe("Asia/Tokyo");
  });

  it("accepts an explicit null timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: null });
    expect(parsed.timezone).toBe(null);
  });

  // Wrong-type drift: a future server bug sending `timezone` as a number
  // must not throw into the UI. parseWithFallback degrades the whole user
  // object to the explicit fallback (EMPTY_USER) so /api/me callers keep a
  // valid shape instead of white-screening.
  it("falls back to EMPTY_USER when timezone is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...base, timezone: 42 },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).toBe(EMPTY_USER);
  });
});

describe("SquadListSchema member preview drift", () => {
  const baseSquad = {
    id: "squad-1",
    workspace_id: "ws-1",
    name: "Frontend Squad",
    description: "",
    instructions: "",
    avatar_url: null,
    leader_id: "agent-1",
    creator_id: "user-1",
    created_at: "2026-05-01T00:00:00Z",
    updated_at: "2026-05-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };

  it("defaults preview fields when an older backend omits them", () => {
    const parsed = SquadListSchema.parse([baseSquad]);
    expect(parsed[0]?.member_count).toBe(0);
    expect(parsed[0]?.member_preview).toEqual([]);
  });

  it("defaults preview fields on a single squad response", () => {
    const parsed = SquadSchema.parse(baseSquad);
    expect(parsed.member_count).toBe(0);
    expect(parsed.member_preview).toEqual([]);
  });

  it("preserves lightweight member preview rows", () => {
    const parsed = SquadListSchema.parse([
      {
        ...baseSquad,
        member_count: 2,
        member_preview: [
          { member_type: "agent", member_id: "agent-1", role: "leader" },
          { member_type: "member", member_id: "user-2", role: "member" },
        ],
      },
    ]);
    expect(parsed[0]?.member_count).toBe(2);
    expect(parsed[0]?.member_preview).toHaveLength(2);
    expect(parsed[0]?.member_preview?.[0]?.role).toBe("leader");
  });
});

// The workspace dashboard and runtime-detail pages were re-pointed at the
// unified `task_usage_hourly` rollup. Every numeric field drives chart /
// KPI math, and string keys (date / agent_id / model) bucket the series.
// The contract these schemas must hold: a row missing a field degrades
// that field to a sane default rather than dropping the WHOLE array to
// the `[]` fallback — one drifted row must not blank the entire chart.
describe("dashboard + runtime usage schema drift", () => {
  it("coerces a missing numeric field to 0 instead of dropping the array", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { date: "2026-05-19", model: "claude-opus-4-7", input_tokens: 100 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.output_tokens).toBe(0);
    expect(parsed[0]?.cache_read_tokens).toBe(0);
    expect(parsed[0]?.cache_write_tokens).toBe(0);
  });

  it("coerces a missing date key to \"\" so the rest of the series survives", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 5 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.date).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the agent-runtime panel", () => {
    const parsed = DashboardAgentRunTimeListSchema.parse([
      { total_seconds: 42, task_count: 3, failed_count: 0 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the usage-by-agent panel", () => {
    const parsed = DashboardUsageByAgentListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 7 },
    ]);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces missing fields on every runtime usage schema", () => {
    expect(RuntimeUsageListSchema.parse([{ date: "2026-05-19" }])[0]?.input_tokens).toBe(0);
    expect(RuntimeHourlyActivityListSchema.parse([{ hour: 9 }])[0]?.count).toBe(0);
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.agent_id).toBe("");
    expect(RuntimeUsageByHourListSchema.parse([{ hour: 9 }])[0]?.model).toBe("");
  });

  it("defaults a missing provider to \"\" so an older server's rows still price by bare model", () => {
    // provider was added for cross-provider model disambiguation; a server
    // predating it omits the field. The schema must fill "" (→ bare-model
    // pricing lookup) rather than drop the row.
    expect(
      DashboardUsageDailyListSchema.parse([{ date: "2026-05-19", model: "claude-opus-4-7" }])[0]
        ?.provider,
    ).toBe("");
    expect(
      DashboardUsageByAgentListSchema.parse([{ model: "claude-opus-4-7" }])[0]?.provider,
    ).toBe("");
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.provider).toBe("");
  });

  it("rejects a non-array body so parseWithFallback can return its fallback", () => {
    expect(DashboardUsageDailyListSchema.safeParse(null).success).toBe(false);
    expect(RuntimeUsageListSchema.safeParse({ rows: [] }).success).toBe(false);
  });

  it("keeps unknown server-side fields via .loose()", () => {
    const parsed = RuntimeUsageListSchema.parse([
      { date: "2026-05-19", region: "us-east" },
    ]);
    expect((parsed[0] as Record<string, unknown>).region).toBe("us-east");
  });
});

describe("AgentFixRecordListSchema drift (Operations tab)", () => {
  it("fills missing fields with defaults so a sparse row still renders", () => {
    const parsed = AgentFixRecordListSchema.parse([
      { task_id: "t1", agent_id: "a1", issue_title: "Fix it", issue_status: "done" },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.agent_name).toBe("");
    expect(parsed[0]?.issue_identifier).toBe("");
    expect(parsed[0]?.last_comment).toBe("");
    expect(parsed[0]?.last_comment_author_type).toBe("");
    // Nullable timestamps default to null, never undefined, so downstream
    // `=== null` checks behave.
    expect(parsed[0]?.started_at).toBeNull();
    expect(parsed[0]?.completed_at).toBeNull();
  });

  it("accepts an unknown issue_status string (enum drift downgrades, not crashes)", () => {
    const parsed = AgentFixRecordListSchema.parse([
      { task_id: "t1", issue_status: "triaged" },
    ]);
    expect(parsed[0]?.issue_status).toBe("triaged");
  });

  it("degrades to the fallback via parseWithFallback on a non-array body", () => {
    expect(AgentFixRecordListSchema.safeParse(null).success).toBe(false);
    const parsed = parseWithFallback(
      { rows: [] },
      AgentFixRecordListSchema,
      [],
      { endpoint: "GET /api/operations/agent-fixes (test)" },
    );
    expect(parsed).toEqual([]);
  });

  it("keeps the last_comment text and author type", () => {
    // The endpoint now returns the agent's own latest comment (member replies
    // are excluded server-side), but the schema stays lenient on author_type —
    // a string, no enum narrowing — so a future value can't white-screen.
    const parsed = AgentFixRecordListSchema.parse([
      { task_id: "t1", last_comment: "looks good", last_comment_author_type: "agent" },
    ]);
    expect(parsed[0]?.last_comment).toBe("looks good");
    expect(parsed[0]?.last_comment_author_type).toBe("agent");
  });

  it("keeps agent_comment_count absent when missing or malformed", () => {
    // Old servers omit the field; the dashboard falls back to last_comment.
    // The distinction "absent" vs 0 must survive parsing, and a drifted type
    // degrades to absent instead of dropping the row.
    const parsed = AgentFixRecordListSchema.parse([
      { task_id: "t1" },
      { task_id: "t2", agent_comment_count: 3 },
      { task_id: "t3", agent_comment_count: "many" },
    ]);
    expect(parsed).toHaveLength(3);
    expect(parsed[0]?.agent_comment_count).toBeUndefined();
    expect(parsed[1]?.agent_comment_count).toBe(3);
    expect(parsed[2]?.agent_comment_count).toBeUndefined();
  });

  it("keeps optional P4 assessment fields while tolerating unknown enum strings", () => {
    const parsed = AgentFixRecordListSchema.parse([
      {
        task_id: "t1",
        external: {
          binding_id: "binding-1",
          work_item_id: "BUG-93218",
          status: "Done",
          mapped_status: "done",
          done: true,
          project: "Warpath3",
        },
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: "future_attribution",
          quality_prediction: "future_quality",
          confidence: 0.86,
          workstream: "rel_1.7.2/server",
          swarm_reviews: [
            {
              id: 11872,
              review_id: "SW-11872",
              changes: [282941],
              commits: [283006],
              swarm_branch: "main",
              event_type: "review.committed",
              sent_at: "2026-06-29T04:05:06Z",
            },
          ],
          ai_shelved_cls: [282941],
          external_committed_cls: [283006],
        },
        human_review: {
          outcome: "future_outcome",
          reasons: ["future_reason"],
        },
        display_result_status: "future_display_status",
        ai_judgement_eval: "future_eval",
      },
    ]);
    expect(parsed[0]?.external?.binding_id).toBe("binding-1");
    expect(parsed[0]?.external?.work_item_id).toBe("BUG-93218");
    expect(parsed[0]?.p4_assessment?.workstream).toBe("rel_1.7.2/server");
    expect(parsed[0]?.p4_assessment?.delivery_attribution_prediction).toBe(
      "future_attribution",
    );
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.review_id).toBe(
      "SW-11872",
    );
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.id).toBe(11872);
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.changes).toEqual([
      282941,
    ]);
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.commits).toEqual([
      283006,
    ]);
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.swarm_branch).toBe("main");
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.event_type).toBe(
      "review.committed",
    );
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.sent_at).toBe(
      "2026-06-29T04:05:06Z",
    );
    expect(parsed[0]?.human_review?.outcome).toBe("future_outcome");
    expect(parsed[0]?.ai_judgement_eval).toBe("future_eval");
  });

  it("normalizes missing and null P4 evidence arrays to stable empty arrays", () => {
    const parsed = AgentFixRecordListSchema.parse([
      {
        task_id: "t1",
        p4_assessment: {
          swarm_reviews: [
            {
              review_id: "SW-11872",
              changes: null,
              commits: null,
              swarm_branch: null,
              event_type: null,
              sent_at: null,
            },
          ],
          ai_shelved_cls: null,
          swarm_change_cls: null,
          swarm_committed_cls: null,
          external_committed_cls: null,
          prediction_reasons: null,
          warnings: null,
        },
      },
    ]);

    const p4 = parsed[0]?.p4_assessment;
    expect(p4?.swarm_reviews?.[0]?.changes).toEqual([]);
    expect(p4?.swarm_reviews?.[0]?.commits).toEqual([]);
    expect(p4?.swarm_reviews?.[0]?.swarm_branch).toBe("");
    expect(p4?.swarm_reviews?.[0]?.event_type).toBe("");
    expect(p4?.swarm_reviews?.[0]?.sent_at).toBe("");
    expect(p4?.ai_shelved_cls).toEqual([]);
    expect(p4?.swarm_change_cls).toEqual([]);
    expect(p4?.swarm_committed_cls).toEqual([]);
    expect(p4?.external_committed_cls).toEqual([]);
    expect(p4?.prediction_reasons).toEqual([]);
    expect(p4?.warnings).toEqual([]);
  });

  it("returns the fallback (never throws) when a field has the wrong type", () => {
    // issue_status arriving as a number is a hard schema violation; the UI
    // path must degrade to the fallback rather than throw a white-screen.
    const parsed = parseWithFallback(
      [{ task_id: "t1", issue_status: 123 }],
      AgentFixRecordListSchema,
      [],
      { endpoint: "GET /api/operations/agent-fixes (test)" },
    );
    expect(parsed).toEqual([]);
  });

  it("coerces a non-array CL field (agent emitted a bare string) to [] instead of blanking the row", () => {
    // Regression: an assessment where swarm_reviews[].commits arrived as the
    // string "unknown" failed z.array() and collapsed the ENTIRE feed to the
    // empty fallback ("暂无记录"). The field must degrade to [], row intact.
    const parsed = AgentFixRecordListSchema.parse([
      {
        task_id: "t1",
        issue_status: "done",
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: "human_delivered",
          quality_prediction: "likely_wrong",
          ai_shelved_cls: "unknown",
          swarm_reviews: [
            { review: 259294, status: "pending", commits: "unknown", changes: "n/a" },
          ],
        },
      },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.p4_assessment?.ai_shelved_cls).toEqual([]);
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.commits).toEqual([]);
    expect(parsed[0]?.p4_assessment?.swarm_reviews?.[0]?.changes).toEqual([]);
  });

  it("drops only the malformed row, keeping valid rows (one bad record never blanks the table)", () => {
    const parsed = AgentFixRecordListSchema.parse([
      { task_id: "good-1", issue_status: "done" },
      "garbage-not-an-object",
      { task_id: "bad", issue_status: 123 }, // number where string required
      { task_id: "good-2", issue_status: "in_review" },
    ]);
    expect(parsed.map((r) => r.task_id)).toEqual(["good-1", "good-2"]);
  });

  it("drops non-string prediction_reasons/warnings entries and non-object swarm reviews", () => {
    const parsed = AgentFixRecordListSchema.parse([
      {
        task_id: "t1",
        p4_assessment: {
          prediction_reasons: ["ok", 42, null, "fine"],
          warnings: "not-an-array",
          swarm_reviews: ["junk", { review_id: "SW-1" }, 7],
        },
      },
    ]);
    const p4 = parsed[0]?.p4_assessment;
    expect(p4?.prediction_reasons).toEqual(["ok", "fine"]);
    expect(p4?.warnings).toEqual([]);
    expect(p4?.swarm_reviews).toHaveLength(1);
    expect(p4?.swarm_reviews?.[0]?.review_id).toBe("SW-1");
  });

  it("parses the P4 assessment trigger response with drift-tolerant defaults", () => {
    const parsed = TriggerAgentFixP4AssessmentResponseSchema.parse({
      created: true,
      assessment_id: "assessment-1",
      task_id: "task-1",
    });
    expect(parsed).toEqual({
      created: true,
      reason: "",
      assessment_id: "assessment-1",
      assessment_status: "",
      task_id: "task-1",
    });
    const fallback = parseWithFallback(
      { created: "yes" },
      TriggerAgentFixP4AssessmentResponseSchema,
      { created: false, reason: "", assessment_status: "" },
      { endpoint: "POST /api/operations/agent-fixes/p4-assessments (test)" },
    );
    expect(fallback).toEqual({
      created: false,
      reason: "",
      assessment_status: "",
    });
  });

  it("parses the human review response with drift-tolerant defaults", () => {
    const parsed = AgentFixHumanReviewSchema.parse({
      outcome: "accepted",
      reasons: ["complete_usable"],
    });
    expect(parsed).toEqual({
      outcome: "accepted",
      reasons: ["complete_usable"],
      note: "",
      reviewer_id: "",
      reviewed_at: null,
    });

    const fallback = parseWithFallback(
      { outcome: "accepted", reasons: "complete_usable" },
      AgentFixHumanReviewSchema,
      { outcome: "", reasons: [], note: "", reviewer_id: "", reviewed_at: null },
      { endpoint: "PATCH /api/operations/agent-fixes/:bindingId/review (test)" },
    );
    expect(fallback).toEqual({
      outcome: "",
      reasons: [],
      note: "",
      reviewer_id: "",
      reviewed_at: null,
    });
  });
});

describe("FeishuProjectIntegrationSchema", () => {
  it("defaults sync_only_workspace_member_items to false when the field is absent (older server)", () => {
    // An older backend won't send the field. parseWithFallback must keep the
    // integration usable, defaulting the assignee-scope gate to off.
    const parsed = parseWithFallback(
      { project_name: "proj", project_key: "proj" },
      FeishuProjectIntegrationSchema,
      EMPTY_FEISHU_PROJECT_INTEGRATION,
      { endpoint: "GET /api/.../feishu-project (test)" },
    );
    expect(parsed.sync_only_workspace_member_items).toBe(false);
  });

  it("parses sync_only_workspace_member_items=true through", () => {
    const parsed = FeishuProjectIntegrationSchema.parse({
      project_name: "proj",
      sync_only_workspace_member_items: true,
    });
    expect(parsed.sync_only_workspace_member_items).toBe(true);
  });
});

describe("AppConfigSchema cdn_signed drift", () => {
  it("defaults cdn_signed to false when the server omits it (pre-MUL-3254 servers)", () => {
    const parsed = AppConfigSchema.parse({ cdn_domain: "cdn.example.com" });
    expect(parsed.cdn_signed).toBe(false);
  });

  it("coerces a malformed cdn_signed to false instead of failing the whole config", () => {
    const parsed = AppConfigSchema.parse({
      cdn_domain: "cdn.example.com",
      cdn_signed: "yes",
    });
    expect(parsed.cdn_signed).toBe(false);
    expect(parsed.cdn_domain).toBe("cdn.example.com");
  });

  it("keeps cdn_signed=true from a signing-enabled server", () => {
    const parsed = AppConfigSchema.parse({ cdn_signed: true });
    expect(parsed.cdn_signed).toBe(true);
  });

  it("parses frontend feature flag decisions", () => {
    const parsed = AppConfigSchema.parse({
      feature_flags: {
        composio_mcp_apps: true,
        malformed_future_flag: "yes",
      },
    });
    expect(parsed.feature_flags).toEqual({
      composio_mcp_apps: true,
      malformed_future_flag: false,
    });
  });

  it("defaults malformed feature_flags to an empty object", () => {
    const parsed = AppConfigSchema.parse({ feature_flags: ["not", "an", "object"] });
    expect(parsed.feature_flags).toEqual({});
  });
});

describe("InboxUnreadSummarySchema", () => {
  const ENDPOINT = { endpoint: "GET /api/inbox/unread-summary" };

  it("parses a well-formed summary and tolerates extra fields", () => {
    const parsed = parseWithFallback(
      [
        { workspace_id: "ws-1", count: 2 },
        { workspace_id: "ws-2", count: 0, future_field: "ignored" },
      ],
      InboxUnreadSummarySchema,
      EMPTY_INBOX_UNREAD_SUMMARY,
      ENDPOINT,
    );
    expect(parsed).toEqual([
      { workspace_id: "ws-1", count: 2 },
      { workspace_id: "ws-2", count: 0, future_field: "ignored" },
    ]);
  });

  it("returns the empty fallback (dot hidden) for a non-array body", () => {
    expect(
      parseWithFallback({ rows: [] }, InboxUnreadSummarySchema, EMPTY_INBOX_UNREAD_SUMMARY, ENDPOINT),
    ).toBe(EMPTY_INBOX_UNREAD_SUMMARY);
    expect(
      parseWithFallback(null, InboxUnreadSummarySchema, EMPTY_INBOX_UNREAD_SUMMARY, ENDPOINT),
    ).toBe(EMPTY_INBOX_UNREAD_SUMMARY);
  });

  it("returns the empty fallback when an entry has a wrong-typed count", () => {
    expect(
      parseWithFallback(
        [{ workspace_id: "ws-1", count: "lots" }],
        InboxUnreadSummarySchema,
        EMPTY_INBOX_UNREAD_SUMMARY,
        ENDPOINT,
      ),
    ).toBe(EMPTY_INBOX_UNREAD_SUMMARY);
  });
});

describe("WorkspaceCapabilitySchema", () => {
  const ENDPOINT = { endpoint: "GET /api/workspaces/:id/capabilities/:capability" };

  it("parses a well-formed capability and tolerates unknown extra fields", () => {
    const parsed = parseWithFallback(
      {
        capability: "p4_assessment",
        agent_id: "agent-1",
        agent_name: "Assessor",
        project_id: null,
        max_concurrent_tasks: 2,
        created_at: "2026-07-01T00:00:00Z",
        future_field: "ignored",
      },
      WorkspaceCapabilitySchema,
      EMPTY_WORKSPACE_CAPABILITY,
      ENDPOINT,
    );
    expect(parsed.agent_id).toBe("agent-1");
    expect(parsed.agent_name).toBe("Assessor");
    expect(parsed.max_concurrent_tasks).toBe(2);
  });

  it("defaults optional fields missing from an older backend", () => {
    const parsed = parseWithFallback(
      { capability: "p4_assessment", agent_id: "agent-1" },
      WorkspaceCapabilitySchema,
      EMPTY_WORKSPACE_CAPABILITY,
      ENDPOINT,
    );
    expect(parsed.agent_name).toBe("");
    expect(parsed.project_id).toBeNull();
    expect(parsed.max_concurrent_tasks).toBe(1);
  });

  it("returns the empty fallback for a wrong-typed body (renders as not configured)", () => {
    expect(
      parseWithFallback(
        { capability: "p4_assessment", agent_id: 42 },
        WorkspaceCapabilitySchema,
        EMPTY_WORKSPACE_CAPABILITY,
        ENDPOINT,
      ),
    ).toBe(EMPTY_WORKSPACE_CAPABILITY);
    expect(
      parseWithFallback(null, WorkspaceCapabilitySchema, EMPTY_WORKSPACE_CAPABILITY, ENDPOINT),
    ).toBe(EMPTY_WORKSPACE_CAPABILITY);
  });
});
