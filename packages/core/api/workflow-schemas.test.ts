import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import {
  EMPTY_WORKFLOW_INSTANCE_DETAIL,
  EMPTY_WORKFLOW_NODE_CONTEXT,
  WorkflowNodeContextSchema,
  ListBuiltinWorkflowTemplatesResponseSchema,
  ListWorkflowsResponseSchema,
  WorkflowInstanceDetailSchema,
  WorkflowDefinitionSchema,
  WorkflowNodeDefinitionSchema,
  WorkflowNodeDetailSchema,
} from "./workflow-schemas";

const endpoint = { endpoint: "GET /api/workflow-instances/:id" };

function instanceShape() {
  return {
    id: "instance-1",
    workspace_id: "workspace-1",
    workflow_id: "template-1",
    workflow_version_id: "version-1",
    host_issue_id: "issue-1",
  };
}

describe("workflow response schemas", () => {
  it("keeps standalone runs compatible with the string host_issue_id contract", () => {
    const parsed = WorkflowInstanceDetailSchema.parse({
      instance: { ...instanceShape(), host_issue_id: "", title: "Standalone run" },
      role_assignments: [],
      nodes: [],
      tasks: [],
    });

    expect(parsed.instance.host_issue_id).toBe("");
    expect(parsed.instance.title).toBe("Standalone run");
  });

  it("accepts a direct execution task backed by a node definition", () => {
    const parsed = WorkflowInstanceDetailSchema.parse({
      instance: { ...instanceShape(), host_issue_id: "", title: "Standalone run" },
      role_assignments: [],
      nodes: [],
      tasks: [{
        id: "task-direct",
        workflow_node_instance_id: "node-1",
        task_key: "execution",
        source: "execution",
        required: true,
        definition: { key: "diagnosis", kind: "activity", name: "Run diagnosis" },
        issue_id: null,
        executor_resolution_id: "resolution-1",
      }],
    });

    expect(parsed.tasks[0]?.definition).toMatchObject({
      key: "diagnosis",
      name: "Run diagnosis",
    });
  });

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
        workflow_name: "Delivery",
        workflow_version: 3,
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
      executions: "invalid",
      submissions: "invalid",
      verdicts: null,
      participants: null,
      executor_resolutions: null,
      confirmations: null,
    });

    expect(parsed.tasks).toEqual([]);
    expect(parsed.executions).toEqual([]);
    expect(parsed.submissions).toEqual([]);
    expect(parsed.verdicts).toEqual([]);
    expect(parsed.participants).toEqual([]);
    expect(parsed.executor_resolutions).toEqual([]);
  });

  it("normalizes malformed recent workflow runs without losing the workflow", () => {
    const parsed = ListWorkflowsResponseSchema.parse({
      workflows: [{
        id: "workflow-1",
        workspace_id: "workspace-1",
        recent_runs: "invalid",
      }],
    });

    expect(parsed.workflows[0]?.recent_runs).toEqual([]);
  });

  it("preserves the node executor and its single fallback", () => {
    const parsed = WorkflowNodeDefinitionSchema.parse({
      key: "backend",
      kind: "activity",
      name: "Backend development",
      executor: {
        kind: "actor",
        actor_type: "agent",
        actor_id: "agent-default",
        fallback: { kind: "manual" },
      },
      reviewer: { kind: "role", role: "qa", required: true },
      issue_templates: [{
        key: "verify",
        title: "Verify implementation",
        required: true,
      }],
    });

    expect(parsed.executor).toMatchObject({
      kind: "actor",
      actor_type: "agent",
      actor_id: "agent-default",
      fallback: { kind: "manual" },
    });
    expect(parsed.reviewer).toMatchObject({ kind: "role", role: "qa", required: true });
  });

  it("keeps a control node that carries an empty executor object", () => {
    // Go's omitempty does not apply to structs, so a start or end node used to
    // arrive as {"executor":{}}. A required kind turned that into a whole-
    // response validation failure and a blank canvas.
    const parsed = WorkflowNodeDefinitionSchema.parse({
      key: "start",
      kind: "start",
      name: "Start",
      executor: {},
      completion: {},
    });

    expect(parsed.key).toBe("start");
    expect(parsed.executor?.kind).toBeUndefined();
  });

  it("downgrades an executor or reviewer whose kind is unknown", () => {
    const parsed = WorkflowNodeDefinitionSchema.parse({
      key: "work",
      kind: "activity",
      name: "Work",
      executor: { kind: "round_robin", role: "owner" },
      reviewer: { kind: "quorum", role: "qa" },
    });

    expect(parsed.executor?.kind).toBe("round_robin");
    expect(parsed.reviewer?.kind).toBe("quorum");
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

  it("falls back on malformed builtin template lists and defaults entry fields", () => {
    const malformed = parseWithFallback(
      { templates: { not: "an array" } },
      ListBuiltinWorkflowTemplatesResponseSchema,
      { templates: [] },
      { endpoint: "GET /api/workflows/builtin" },
    );
    expect(malformed.templates).toEqual([]);

    const partial = ListBuiltinWorkflowTemplatesResponseSchema.parse({
      templates: [{ key: "bug_fix" }],
    });
    expect(partial.templates[0]?.name).toBe("");
    expect(partial.templates[0]?.description).toBe("");
  });

  // Output fields and gateway cases are what routing reads; a node whose
  // schema drifted must degrade to "no fields declared" rather than take the
  // whole definition down with it.
  it("keeps declared output fields and gateway cases, defaulting absent parts", () => {
    const parsed = WorkflowNodeDefinitionSchema.parse({
      key: "triage",
      kind: "activity",
      outputs: [
        { key: "is_bug", type: "bool", required: true, desc: "real defect" },
        { key: "category", type: "enum", values: ["bug", "duplicate"] },
      ],
    });
    expect(parsed.outputs?.[0]?.required).toBe(true);
    expect(parsed.outputs?.[1]?.values).toEqual(["bug", "duplicate"]);
    // Absent optional parts get usable defaults instead of undefined.
    expect(parsed.outputs?.[1]?.required).toBe(false);
    expect(parsed.outputs?.[1]?.desc).toBe("");

    const gateway = WorkflowNodeDefinitionSchema.parse({
      key: "route",
      kind: "gateway",
      cases: [
        { id: "c1", label: "not a defect", when: 'category == "duplicate"' },
        { id: "else", label: "continue" },
      ],
    });
    expect(gateway.cases?.map((entry) => entry.id)).toEqual(["c1", "else"]);
    // The else case carries no condition; it must read as empty, not missing.
    expect(gateway.cases?.[1]?.when).toBe("");
  });

  it("survives a malformed outputs array and unknown field types", () => {
    const malformed = WorkflowNodeDefinitionSchema.parse({
      key: "triage",
      kind: "activity",
      outputs: { not: "an array" },
    });
    expect(malformed.outputs).toEqual([]);

    // A type this build has never heard of stays a string rather than
    // failing the node — the form falls back to a text input.
    const future = WorkflowNodeDefinitionSchema.parse({
      key: "triage",
      kind: "activity",
      outputs: [{ key: "score", type: "decimal" }],
    });
    expect(future.outputs?.[0]?.type).toBe("decimal");
  });

  it("carries the gateway case binding on edges", () => {
    const parsed = WorkflowDefinitionSchema.parse({
      name: "branching",
      nodes: [],
      edges: [
        { from: "route", to: "end", from_case: "c1" },
        { from: "start", to: "route" },
      ],
    });
    expect(parsed.edges[0]?.from_case).toBe("c1");
    expect(parsed.edges[1]?.from_case).toBeUndefined();
  });

  // A run whose reviewer node produced a critic task rendered as an empty
  // workbench: no nodes, no host issue, status "unknown". Nothing was wrong
  // with the run — the whole detail response failed validation on one task's
  // definition snapshot and fell back to the empty instance, and the fallback
  // is silent by design. One optional panel's shape took down the page.
  it("keeps a run readable when a critic task carries a reviewer snapshot", () => {
    const parsed = parseWithFallback(
      {
        instance: { ...instanceShape(), status: "completed", title: "Bug fix" },
        role_assignments: [],
        nodes: [],
        tasks: [
          {
            id: "task-critic",
            workflow_node_instance_id: "node-1",
            task_key: "critic",
            source: "critic",
            // The server snapshots the reviewer block here. It has no `key`,
            // which both members of the definition union require.
            definition: { kind: "reviewer", role: "qa", required: true },
            materialization_status: "materialized",
          },
        ],
      },
      WorkflowInstanceDetailSchema,
      EMPTY_WORKFLOW_INSTANCE_DETAIL,
      endpoint,
    );

    expect(parsed.instance.status).toBe("completed");
    expect(parsed.instance.host_issue_id).toBe("issue-1");
    expect(parsed.tasks).toHaveLength(1);
    expect(parsed.tasks[0]?.task_key).toBe("critic");
  });

  // The node context panel renders on an ordinary issue page. A partial or
  // wrong-typed payload has to degrade to empty collections rather than throw,
  // or a server change takes the whole issue view down with it.
  it("keeps a node context readable when its collections are missing or wrong", () => {
    const parsed = parseWithFallback(
      {
        instance_id: "run-1",
        node_key: "implement",
        node_name: "Implementation",
        // The server sends arrays here; a null and an object are what a
        // partial rollout or a serialisation bug actually produces.
        artifacts: null,
        upstream: { node_key: "design" },
        outputs: undefined,
      },
      WorkflowNodeContextSchema,
      EMPTY_WORKFLOW_NODE_CONTEXT,
      { endpoint: "GET /api/issues/:id/workflow-node" },
    );

    expect(parsed.node_key).toBe("implement");
    expect(parsed.artifacts).toEqual([]);
    expect(parsed.upstream).toEqual([]);
    expect(parsed.outputs).toEqual([]);
    expect(parsed.host_issue).toBe("");
  });

  // summary and worker_output are different claims — one authored, one
  // extracted — and the panel labels them differently, so the schema must not
  // let one stand in for the other.
  it("keeps an upstream conclusion apart from an extracted output", () => {
    const parsed = WorkflowNodeContextSchema.parse({
      node_key: "implement",
      upstream: [
        { node_key: "design", summary: "Two edge cases stay open." },
        { node_key: "review", worker_output: "Ran the importer." },
      ],
    });

    expect(parsed.upstream[0]?.summary).toBe("Two edge cases stay open.");
    expect(parsed.upstream[0]?.worker_output).toBe("");
    expect(parsed.upstream[1]?.summary).toBe("");
    expect(parsed.upstream[1]?.worker_output).toBe("Ran the importer.");
  });
});
