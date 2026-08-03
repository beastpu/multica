import { expect, test } from "@playwright/test";

import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

// The delivery contract end to end: a node declares what it owes, the run
// refuses to advance without it, and both halves — the artifact and the handoff
// summary — are visible to a person rather than only to an agent.

let api: TestApiClient;

const definition = {
  schema_version: 1,
  name: "E2E delivery",
  roles: [{
    key: "owner",
    name: "Owner",
    required: true,
    allowed_actor_types: ["member"],
  }],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    {
      key: "design",
      kind: "activity",
      activity_mode: "work",
      name: "Design",
      owner_role: "owner",
      issue_policy: "none",
      executor: {
        strategies: [{ kind: "fixed_role", role: "owner" }, { kind: "manual" }],
      },
      artifacts: [{
        key: "design_doc",
        name: "Technical design",
        description: "Implementation path and verification",
        kind: "document",
        required: true,
      }],
      completion: {
        mode: "manual",
        required_issue_outcome: "none",
        handoff_required: true,
      },
    },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "design" },
    { from: "design", to: "end" },
  ],
  acceptance: { policy: "none", rework_targets: [] },
};

test.beforeEach(async ({ page }) => {
  api = await createTestApi();
  await loginAsDefault(page);
});

test.afterEach(async () => {
  await api.cleanup();
});

test("a member sees what a node owes and can read what it delivered", async ({ page }) => {
  const issue = await api.createIssue("E2E workflow artifact host", {
    description: "Import users from CSV, validating each row.",
  });
  const templateId = await api.publishWorkflow("E2E delivery", definition);
  const started = await api.startWorkflow(issue.id, templateId, [{
    role_key: "owner",
    actor_type: "member",
    actor_id: await api.getUserId(),
  }]);

  const designNode = started.nodes?.find(
    (node: { node_key: string }) => node.node_key === "design",
  );
  expect(
    designNode,
    `the run should have activated the design node; got ${JSON.stringify(started).slice(0, 400)}`,
  ).toBeTruthy();

  await page.goto(`/issues/${issue.id}`);

  // Before anything is delivered the panel names the outstanding requirement,
  // which is what makes a blocked node self-explanatory rather than stuck.
  await page.getByRole("tab", { name: /Artifacts/i }).click();
  await expect(page.getByText("Technical design")).toBeVisible();
  await expect(
    page.getByText("Required — not submitted yet."),
  ).toBeVisible();

  await api.submitWorkflowArtifact(designNode.id, {
    artifact_key: "design_doc",
    content: "## Design\n\nStream the CSV and validate per row.",
  });

  await page.reload();
  await page.getByRole("tab", { name: /Artifacts/i }).click();
  await page.getByRole("button", { name: /Read/i }).click();
  await expect(page.getByText("Stream the CSV and validate per row.")).toBeVisible();
});

test("a node with no issues still offers a handoff summary", async ({ page }) => {
  const issue = await api.createIssue("E2E workflow handoff host", {
    description: "Ship the import feature.",
  });
  const templateId = await api.publishWorkflow("E2E handoff", definition);
  await api.startWorkflow(issue.id, templateId, [{
    role_key: "owner",
    actor_type: "member",
    actor_id: await api.getUserId(),
  }]);

  await page.goto(`/issues/${issue.id}`);
  await page.getByRole("tab", { name: /Completion form/i }).click();

  // issue_policy "none" produces no child issue, so this panel is the only
  // place a person can hand anything off from.
  await expect(page.getByLabel("Summary")).toBeVisible();
});
