// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentTask } from "@multica/core/types/agent";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowExecutionHistory } from "./workflow-execution-history";

vi.mock("../common/task-transcript", () => ({
  TranscriptButton: ({ label, task }: { label: string; task: AgentTask }) => (
    <button type="button" data-task-id={task.id}>{label}</button>
  ),
}));

const baseExecution: AgentTask = {
  id: "task-1",
  agent_id: "agent-1",
  runtime_id: "runtime-1",
  issue_id: "",
  status: "running",
  priority: 0,
  dispatched_at: null,
  started_at: "2026-08-03T10:00:00.000Z",
  completed_at: null,
  result: null,
  error: null,
  created_at: "2026-08-03T09:59:00.000Z",
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      {children}
    </I18nProvider>
  );
}

describe("WorkflowExecutionHistory", () => {
  it("renders worker attempts with a transcript entry", () => {
    render(
      <WorkflowExecutionHistory
        executions={[
          baseExecution,
          {
            ...baseExecution,
            id: "task-2",
            status: "failed",
            attempt: 2,
          },
        ]}
        agentName={(agentId) =>
          agentId === "agent-1" ? "Build agent" : agentId}
      />,
      { wrapper: Wrapper },
    );

    expect(screen.getByText("Execution history")).toBeInTheDocument();
    expect(screen.getAllByText("Build agent")).toHaveLength(2);
    expect(screen.getByText("Attempt 2")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "View log" })).toHaveLength(2);
  });

  it("stays out of the node panel before an execution exists", () => {
    const { container } = render(
      <WorkflowExecutionHistory executions={[]} agentName={(id) => id} />,
      { wrapper: Wrapper },
    );

    expect(container).toBeEmptyDOMElement();
  });
});
