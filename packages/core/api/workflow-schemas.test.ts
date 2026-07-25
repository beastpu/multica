import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import {
  EMPTY_WORKFLOW_INSTANCE_DETAIL,
  WorkflowInstanceDetailSchema,
  WorkflowNodeDefinitionSchema,
  WorkflowNodeDetailSchema,
  WorkflowSubmissionSchema,
} from "./workflow-schemas";

const endpoint = { endpoint: "GET /api/workflow-instances/:id" };

function instanceShape() {
  return {
    id: "instance-1",
    workspace_id: "workspace-1",
    template_id: "template-1",
    template_version_id: "version-1",
    host_issue_id: "issue-1",
  };
}

describe("workflow response schemas", () => {
  it("normalizes null and wrong-typed collection fields to empty arrays", () => {
    const parsed = WorkflowInstanceDetailSchema.parse({
      instance: {
        ...instanceShape(),
        current_activities: null,
        current_owners: "older-server-shape",
      },
      role_assignments: null,
      nodes: "older-server-shape",
      tasks: { not: "an array" },
    });

    expect(parsed.role_assignments).toEqual([]);
    expect(parsed.nodes).toEqual([]);
    expect(parsed.tasks).toEqual([]);
    expect(parsed.instance.current_activities).toEqual([]);
    expect(parsed.instance.current_owners).toEqual([]);
  });

  it("preserves the run-card display context and defaults missing fields", () => {
    const parsed = WorkflowInstanceDetailSchema.parse({
      instance: {
        ...instanceShape(),
        host_issue_title: "Ship Workflow",
        host_issue_identifier: "MUL-42",
        template_name: "Delivery",
        template_version: 3,
        current_activities: [{
          id: "node-1",
          node_key: "implementation",
          name: "Implementation",
          status: "active",
          attempt: 2,
        }],
        activity_completed: 1,
        activity_total: 4,
        current_owners: [{ actor_type: "member", actor_id: "member-1" }],
      },
      role_assignments: [],
      nodes: [],
      tasks: [],
    });

    expect(parsed.instance.host_issue_identifier).toBe("MUL-42");
    expect(parsed.instance.current_activities[0]?.attempt).toBe(2);
    expect(parsed.instance.activity_total).toBe(4);
  });

  it("falls back instead of exposing a wrong-typed instance identity", () => {
    const parsed = parseWithFallback(
      {
        instance: { ...instanceShape(), id: 123 },
        role_assignments: [],
        nodes: [],
        tasks: [],
      },
      WorkflowInstanceDetailSchema,
      EMPTY_WORKFLOW_INSTANCE_DETAIL,
      endpoint,
    );

    expect(parsed).toBe(EMPTY_WORKFLOW_INSTANCE_DETAIL);
  });

  it("normalizes nullable node-detail projections independently", () => {
    const parsed = WorkflowNodeDetailSchema.parse({
      instance: instanceShape(),
      node: {
        id: "node-1",
        workflow_instance_id: "instance-1",
        node_key: "work",
        node_kind: "activity",
        definition: {
          key: "work",
          kind: "activity",
          name: "Work",
        },
      },
      tasks: null,
      submissions: "invalid",
      verdicts: null,
      participants: null,
      executor_resolutions: null,
      confirmations: null,
    });

    expect(parsed.tasks).toEqual([]);
    expect(parsed.submissions).toEqual([]);
    expect(parsed.verdicts).toEqual([]);
    expect(parsed.participants).toEqual([]);
    expect(parsed.executor_resolutions).toEqual([]);
    expect(parsed.confirmations).toEqual([]);
  });

  it("keeps valid proposed tasks and degrades a malformed list to empty", () => {
    const base = {
      id: "submission-1",
      workflow_node_instance_id: "node-1",
      payload: {},
    };
    const valid = WorkflowSubmissionSchema.parse({
      ...base,
      proposed_tasks: [{
        key: "investigate",
        title: "Investigate",
        required: true,
      }],
    });
    const malformed = WorkflowSubmissionSchema.parse({
      ...base,
      proposed_tasks: "not-an-array",
    });

    expect(valid.proposed_tasks).toHaveLength(1);
    expect(valid.proposed_tasks[0]?.key).toBe("investigate");
    expect(malformed.proposed_tasks).toEqual([]);
  });

  it("preserves direct node executors and child issue overrides", () => {
    const parsed = WorkflowNodeDefinitionSchema.parse({
      key: "backend",
      kind: "activity",
      name: "Backend development",
      executor: {
        strategies: [{
          kind: "fixed_actor",
          actor_type: "agent",
          actor_id: "agent-default",
        }, {
          kind: "manual",
        }],
      },
      issue_templates: [{
        key: "verify",
        title: "Verify implementation",
        assignee_type: "squad",
        assignee_id: "squad-review",
        required: true,
      }],
    });

    expect(parsed.executor?.strategies[0]).toMatchObject({
      kind: "fixed_actor",
      actor_type: "agent",
      actor_id: "agent-default",
    });
    expect(parsed.issue_templates[0]).toMatchObject({
      assignee_type: "squad",
      assignee_id: "squad-review",
    });
  });

  it("preserves known completion modes and ignores unknown future modes", () => {
    const manual = WorkflowNodeDefinitionSchema.parse({
      key: "review",
      kind: "activity",
      name: "Review",
      completion: {
        mode: "manual",
        required_issue_outcome: "done",
      },
    });
    const future = WorkflowNodeDefinitionSchema.parse({
      key: "review",
      kind: "activity",
      name: "Review",
      completion: {
        mode: "approval_chain",
        required_issue_outcome: "done",
      },
    });

    expect(manual.completion.mode).toBe("manual");
    expect(future.completion.mode).toBeUndefined();
    expect(future.completion.required_issue_outcome).toBe("done");
  });
});
