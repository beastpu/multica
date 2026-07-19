import { describe, expect, it } from "vitest";
import {
  AgentFixHumanReviewSchema,
  AgentFixRecordListSchema,
  AppConfigSchema,
  AgentTaskListSchema,
  AutopilotRunSchema,
  FALLBACK_AUTOPILOT_RUN,
  CommentTriggerPreviewSchema,
  DashboardAgentRunTimeListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  ChatDraftRestoresResponseSchema,
  CreateFeedbackResponseSchema,
  DuplicateIssueErrorBodySchema,
  EMPTY_FEISHU_PROJECT_INTEGRATION,
  EMPTY_CHAT_DRAFT_RESTORES,
  EMPTY_CREATE_FEEDBACK_RESPONSE,
  EMPTY_INBOX_ITEMS,
  EMPTY_INBOX_UNREAD_SUMMARY,
  EMPTY_SEARCH_PROJECTS_RESPONSE,
  EMPTY_USER,
  FeishuProjectIntegrationSchema,
  InboxItemListSchema,
  InboxUnreadSummarySchema,
  IssueTriggerPreviewSchema,
  ListIssuesResponseSchema,
  ListPropertiesResponseSchema,
  SearchProjectsResponseSchema,
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

  it("accepts custom property values including multi_select arrays", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          properties: { "def-1": "opt-a", "def-2": ["opt-x", "opt-y"], "def-3": 3.5, "def-4": true },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.properties).toEqual({
      "def-1": "opt-a",
      "def-2": ["opt-x", "opt-y"],
      "def-3": 3.5,
      "def-4": true,
    });
  });

  it("defaults properties to {} when the server omits it (older backend)", () => {
    const parsed = ListIssuesResponseSchema.parse({ issues: [baseIssue], total: 1 });
    expect(parsed.issues[0]?.properties).toEqual({});
  });

  it("drops unknown-shaped property values instead of failing the issue parse", () => {
    // Forward compat: a future server type (actor/relation) may ship object
    // values. That one entry must disappear; the issue and its other
    // properties must survive — a full parse failure would blank the whole
    // list through parseWithFallback on installed desktop builds.
    const payload = {
      issues: [
        {
          ...baseIssue,
          properties: { "def-1": { nested: 1 }, "def-2": "opt-a" },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.properties).toEqual({ "def-2": "opt-a" });
  });
});

describe("IssuePropertySchema (via ListPropertiesResponseSchema)", () => {
  const baseProperty = {
    id: "22222222-2222-2222-2222-222222222222",
    workspace_id: "ws-1",
    name: "Severity",
    type: "select",
    description: "",
    icon: "flag",
    config: { options: [{ id: "opt-1", name: "Critical", color: "#ef4444" }] },
    position: 1,
    archived: false,
    archived_at: null,
    usage_count: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };

  it("parses a full definition", () => {
    const parsed = ListPropertiesResponseSchema.parse({ properties: [baseProperty], total: 1 });
    expect(parsed.properties[0]?.config.options?.[0]?.name).toBe("Critical");
    expect(parsed.properties[0]?.icon).toBe("flag");
  });

  it("survives a malformed response by defaulting the list", () => {
    const parsed = ListPropertiesResponseSchema.parse({});
    expect(parsed.properties).toEqual([]);
    expect(parsed.total).toBe(0);
  });

  it("keeps unknown property types as strings (forward compat)", () => {
    const parsed = ListPropertiesResponseSchema.parse({
      properties: [{ ...baseProperty, type: "relation", config: {} }],
      total: 1,
    });
    expect(parsed.properties[0]?.type).toBe("relation");
  });

  it("defaults config when the server sends none", () => {
    const { config: _omit, ...withoutConfig } = baseProperty;
    const parsed = ListPropertiesResponseSchema.parse({ properties: [withoutConfig], total: 1 });
    expect(parsed.properties[0]?.config).toEqual({});
  });

  it("defaults icon for an older server response", () => {
    const { icon: _omit, ...withoutIcon } = baseProperty;
    const parsed = ListPropertiesResponseSchema.parse({ properties: [withoutIcon], total: 1 });
    expect(parsed.properties[0]?.icon).toBe("");
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

describe("AgentTaskListSchema", () => {
  const task = {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "queued",
    priority: 0,
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-10T00:00:00Z",
    trigger_comment_id: "comment-3",
  };

  it("preserves planned and delivered comment IDs for a task run", () => {
    const parsed = AgentTaskListSchema.parse([
      {
        ...task,
        coalesced_comment_ids: ["comment-1", "comment-2"],
        delivered_comment_ids: ["comment-1", "comment-2", "comment-3"],
      },
    ]);

    expect(parsed[0]?.trigger_comment_id).toBe("comment-3");
    expect(parsed[0]?.coalesced_comment_ids).toEqual([
      "comment-1",
      "comment-2",
    ]);
    expect(parsed[0]?.delivered_comment_ids).toEqual([
      "comment-1",
      "comment-2",
      "comment-3",
    ]);
  });

  it("accepts task payloads from older backends without comment coverage", () => {
    const parsed = AgentTaskListSchema.parse([task]);
    expect(parsed[0]?.coalesced_comment_ids).toBeUndefined();
    expect(parsed[0]?.delivered_comment_ids).toBeUndefined();
  });

  it("degrades malformed optional coverage without dropping task rows", () => {
    const parsed = AgentTaskListSchema.parse([
      {
        ...task,
        coalesced_comment_ids: ["comment-1", 2],
        delivered_comment_ids: "not-an-array",
      },
      {
        ...task,
        id: "task-2",
        delivered_comment_ids: ["comment-2", "comment-3"],
      },
    ]);

    expect(parsed).toHaveLength(2);
    expect(parsed[0]?.coalesced_comment_ids).toBeUndefined();
    expect(parsed[0]?.delivered_comment_ids).toBeUndefined();
    expect(parsed[1]?.delivered_comment_ids).toEqual([
      "comment-2",
      "comment-3",
    ]);
  });
});

describe("ChatDraftRestoresResponseSchema", () => {
  it("parses a well-formed response with attachments", () => {
    const parsed = parseWithFallback(
      {
        restores: [
          {
            id: "msg-1",
            chat_session_id: "s-1",
            task_id: "t-1",
            content: "run the thing",
            attachments: [{ id: "att-1", filename: "notes.txt" }],
            created_at: "2026-07-01T00:00:00Z",
          },
        ],
      },
      ChatDraftRestoresResponseSchema,
      EMPTY_CHAT_DRAFT_RESTORES,
      { endpoint: "test" },
    );
    expect(parsed.restores).toHaveLength(1);
    expect(parsed.restores[0]?.content).toBe("run the thing");
    expect(parsed.restores[0]?.attachments?.[0]?.id).toBe("att-1");
  });

  it("defaults a missing restores array instead of crashing the composer", () => {
    const parsed = parseWithFallback(
      {},
      ChatDraftRestoresResponseSchema,
      EMPTY_CHAT_DRAFT_RESTORES,
      { endpoint: "test" },
    );
    expect(parsed.restores).toEqual([]);
  });

  it("falls back to the empty response on a malformed row", () => {
    // A row without the consume key (id) is unusable — the whole response
    // falls back and the durable rows simply stay pending server-side.
    const parsed = parseWithFallback(
      { restores: [{ chat_session_id: "s-1", content: 42 }] },
      ChatDraftRestoresResponseSchema,
      EMPTY_CHAT_DRAFT_RESTORES,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_CHAT_DRAFT_RESTORES);
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
    expect(parsed[0]?.issue_assignee_type).toBeUndefined();
    expect(parsed[0]?.issue_assignee_id).toBeUndefined();
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

  it("keeps the current issue assignee separate from the latest task agent", () => {
    const parsed = AgentFixRecordListSchema.parse([
      {
        task_id: "task-1",
        agent_id: "historical-agent",
        issue_assignee_type: "agent",
        issue_assignee_id: "current-agent",
      },
      {
        task_id: "task-2",
        agent_id: "historical-agent",
        issue_assignee_type: 42,
        issue_assignee_id: null,
      },
    ]);
    expect(parsed[0]?.issue_assignee_type).toBe("agent");
    expect(parsed[0]?.issue_assignee_id).toBe("current-agent");
    expect(parsed[1]?.issue_assignee_type).toBeUndefined();
    expect(parsed[1]?.issue_assignee_id).toBeUndefined();
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

  it("parses server_version and leaves it undefined when the server omits it", () => {
    expect(AppConfigSchema.parse({ server_version: "1.2.3" }).server_version).toBe("1.2.3");
    expect(AppConfigSchema.parse({}).server_version).toBeUndefined();
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

describe("CloudRuntimeEnvSchema", () => {
  const ENDPOINT = { endpoint: "GET /api/workspaces/:id/cloud-runtime-env" };

  it("parses a configured response, keeping non-sensitive values only", async () => {
    const { CloudRuntimeEnvSchema, EMPTY_CLOUD_RUNTIME_ENV } = await import("./schemas");
    const parsed = parseWithFallback(
      {
        configured: true,
        env: [
          { name: "OPENAI_API_KEY", last4: "abcd" },
          { name: "CODEX_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" },
        ],
        updated_at: "2026-07-16T00:00:00Z",
      },
      CloudRuntimeEnvSchema,
      EMPTY_CLOUD_RUNTIME_ENV,
      ENDPOINT,
    );
    expect(parsed.configured).toBe(true);
    expect(parsed.env).toHaveLength(2);
    expect(parsed.env[0]).toEqual({ name: "OPENAI_API_KEY", last4: "abcd" });
    expect(parsed.env[1]).toEqual({
      name: "CODEX_BASE_URL",
      last4: "/v1",
      value: "https://proxy.example/v1",
    });
  });

  it("degrades missing/null fields instead of throwing", async () => {
    const { CloudRuntimeEnvSchema, EMPTY_CLOUD_RUNTIME_ENV } = await import("./schemas");
    // env array missing entirely -> defaults to []
    const noEnv = parseWithFallback(
      { configured: true },
      CloudRuntimeEnvSchema,
      EMPTY_CLOUD_RUNTIME_ENV,
      ENDPOINT,
    );
    expect(noEnv.env).toEqual([]);
    // entry missing last4 -> defaults to ""
    const noLast4 = parseWithFallback(
      { configured: true, env: [{ name: "OPENAI_API_KEY" }] },
      CloudRuntimeEnvSchema,
      EMPTY_CLOUD_RUNTIME_ENV,
      ENDPOINT,
    );
    expect(noLast4.env[0]).toEqual({ name: "OPENAI_API_KEY", last4: "" });
  });

  it("returns the empty fallback for a wrong-typed or null body", async () => {
    const { CloudRuntimeEnvSchema, EMPTY_CLOUD_RUNTIME_ENV } = await import("./schemas");
    expect(
      parseWithFallback(
        { configured: "yes", env: "nope" },
        CloudRuntimeEnvSchema,
        EMPTY_CLOUD_RUNTIME_ENV,
        ENDPOINT,
      ),
    ).toBe(EMPTY_CLOUD_RUNTIME_ENV);
    expect(
      parseWithFallback(null, CloudRuntimeEnvSchema, EMPTY_CLOUD_RUNTIME_ENV, ENDPOINT),
    ).toBe(EMPTY_CLOUD_RUNTIME_ENV);
  });
});

describe("InboxItemListSchema", () => {
  const ENDPOINT = { endpoint: "GET /api/inbox/archived" };

  const row = (overrides: Record<string, unknown> = {}) => ({
    id: "inbox-1",
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "member-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "Issue title",
    body: null,
    read: false,
    archived: true,
    created_at: "2026-06-15T08:00:00Z",
    ...overrides,
  });

  it("parses a well-formed archived list and tolerates extra fields", () => {
    const parsed = parseWithFallback(
      [row({ issue_status: "in_progress", details: { comment_id: "c-1" }, future_field: 1 })],
      InboxItemListSchema,
      EMPTY_INBOX_ITEMS,
      ENDPOINT,
    );
    expect(parsed).toHaveLength(1);
    expect(parsed[0]).toMatchObject({ id: "inbox-1", archived: true });
  });

  it("keeps a notification type this client doesn't know yet", () => {
    // Enums stay lenient on purpose: a backend that ships a new inbox type
    // must not blank the whole archived list on older clients.
    const parsed = parseWithFallback(
      [row({ type: "some_future_type", severity: "future_severity" })],
      InboxItemListSchema,
      EMPTY_INBOX_ITEMS,
      ENDPOINT,
    );
    expect(parsed).toHaveLength(1);
  });

  it("accepts rows that omit the nullable optional fields", () => {
    const { body, issue_id, ...withoutOptionals } = row();
    void body;
    void issue_id;
    expect(
      parseWithFallback([withoutOptionals], InboxItemListSchema, EMPTY_INBOX_ITEMS, ENDPOINT),
    ).toHaveLength(1);
  });

  it("returns the empty fallback for a non-array body", () => {
    expect(
      parseWithFallback({ items: [] }, InboxItemListSchema, EMPTY_INBOX_ITEMS, ENDPOINT),
    ).toBe(EMPTY_INBOX_ITEMS);
    expect(
      parseWithFallback(null, InboxItemListSchema, EMPTY_INBOX_ITEMS, ENDPOINT),
    ).toBe(EMPTY_INBOX_ITEMS);
  });

  it("returns the empty fallback when a row is missing a required field", () => {
    const { id, ...withoutId } = row();
    void id;
    expect(
      parseWithFallback([withoutId], InboxItemListSchema, EMPTY_INBOX_ITEMS, ENDPOINT),
    ).toBe(EMPTY_INBOX_ITEMS);
  });

  it("returns the empty fallback when `archived` is wrong-typed", () => {
    expect(
      parseWithFallback(
        [row({ archived: "yes" })],
        InboxItemListSchema,
        EMPTY_INBOX_ITEMS,
        ENDPOINT,
      ),
    ).toBe(EMPTY_INBOX_ITEMS);
  });
});

describe("SearchProjectsResponseSchema date drift", () => {
  const ENDPOINT = { endpoint: "GET /api/projects/search" };

  const baseProject = {
    id: "p-1",
    workspace_id: "ws-1",
    title: "Launch",
    description: null,
    icon: null,
    status: "in_progress",
    priority: "high",
    lead_type: null,
    lead_id: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    issue_count: 0,
    done_count: 0,
    resource_count: 0,
    match_source: "title",
  };

  it("parses start_date / due_date when the backend returns them", () => {
    const parsed = parseWithFallback(
      { projects: [{ ...baseProject, start_date: "2026-03-01", due_date: "2026-03-31" }], total: 1 },
      SearchProjectsResponseSchema,
      EMPTY_SEARCH_PROJECTS_RESPONSE,
      ENDPOINT,
    );
    expect(parsed.projects[0]?.start_date).toBe("2026-03-01");
    expect(parsed.projects[0]?.due_date).toBe("2026-03-31");
  });

  // Frontend deploys before backend: an older backend omits the new keys. The
  // .default(null) must keep the whole batch parseable (→ null), not degrade
  // it to the empty fallback and blank the search results.
  it("defaults missing start_date / due_date to null without dropping results", () => {
    const parsed = parseWithFallback(
      { projects: [baseProject], total: 1 },
      SearchProjectsResponseSchema,
      EMPTY_SEARCH_PROJECTS_RESPONSE,
      ENDPOINT,
    );
    expect(parsed).not.toBe(EMPTY_SEARCH_PROJECTS_RESPONSE);
    expect(parsed.projects).toHaveLength(1);
    expect(parsed.projects[0]?.start_date).toBeNull();
    expect(parsed.projects[0]?.due_date).toBeNull();
  });
});

// The "run now" flow branches on run.status/reason_code to avoid a false-success
// toast (MUL-4525), so the trigger response must survive backend drift.
describe("AutopilotRunSchema", () => {
  const ENDPOINT = { endpoint: "POST /api/autopilots/:id/trigger" };
  const baseRun = {
    id: "run-1",
    autopilot_id: "ap-1",
    trigger_id: null,
    source: "manual",
    status: "issue_created",
    issue_id: "issue-1",
    task_id: null,
    triggered_at: "2026-07-14T00:00:00Z",
    completed_at: null,
    failure_reason: null,
    trigger_payload: null,
    result: null,
    created_at: "2026-07-14T00:00:00Z",
  };

  it("preserves a blocked run's status and reason_code", () => {
    const parsed = parseWithFallback(
      { ...baseRun, status: "skipped", failure_reason: "you are not allowed to trigger this autopilot's assignee agent", reason_code: "invocation_not_allowed" },
      AutopilotRunSchema,
      FALLBACK_AUTOPILOT_RUN,
      ENDPOINT,
    );
    expect(parsed.status).toBe("skipped");
    expect(parsed.reason_code).toBe("invocation_not_allowed");
  });

  it("tolerates an older server omitting reason_code", () => {
    const parsed = parseWithFallback(baseRun, AutopilotRunSchema, FALLBACK_AUTOPILOT_RUN, ENDPOINT);
    expect(parsed.status).toBe("issue_created");
    expect(parsed.reason_code).toBeUndefined();
  });

  it("degrades a malformed response to a non-success fallback (never a false success)", () => {
    const parsed = parseWithFallback("not-an-object", AutopilotRunSchema, FALLBACK_AUTOPILOT_RUN, ENDPOINT);
    expect(parsed).toBe(FALLBACK_AUTOPILOT_RUN);
    expect(parsed.status).toBe("failed");
  });
});

// The comment composer branches on preview.blocked to warn before sending
// (MUL-4525 §2), so the additive field must parse and degrade gracefully.
describe("CommentTriggerPreviewSchema.blocked", () => {
  it("parses blocked mention outcomes alongside agents", () => {
    const parsed = CommentTriggerPreviewSchema.parse({
      agents: [{ id: "a1", source: "mention_agent", reason: "" }],
      blocked: [
        { target_type: "squad", target_id: "s1", status: "blocked", reason_code: "invocation_not_allowed" },
      ],
    });
    expect(parsed.agents).toHaveLength(1);
    expect(parsed.blocked).toEqual([
      { target_type: "squad", target_id: "s1", status: "blocked", reason_code: "invocation_not_allowed" },
    ]);
  });

  it("defaults blocked to [] when an older server omits it", () => {
    const parsed = CommentTriggerPreviewSchema.parse({ agents: [] });
    expect(parsed.blocked).toEqual([]);
  });

  it("degrades a malformed blocked field to [] without dropping agents", () => {
    const parsed = CommentTriggerPreviewSchema.parse({
      agents: [{ id: "a1", source: "mention_agent", reason: "" }],
      blocked: "nope",
    });
    expect(parsed.agents).toHaveLength(1);
    expect(parsed.blocked).toEqual([]);
  });

  it("drops a single malformed blocked entry without discarding the valid ones", () => {
    const parsed = CommentTriggerPreviewSchema.parse({
      agents: [],
      blocked: [
        { target_type: "squad", target_id: "s1", status: "blocked", reason_code: "invocation_not_allowed" },
        { status: "blocked" }, // missing target_id → dropped individually
        { target_type: "agent", target_id: "a1", status: "blocked", reason_code: "runtime_offline" },
      ],
    });
    expect(parsed.blocked.map((b) => b.target_id)).toEqual(["s1", "a1"]);
  });
});
