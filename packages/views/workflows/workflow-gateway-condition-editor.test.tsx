// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowGatewayConditionEditor } from "./workflow-gateway-condition-editor";

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

describe("WorkflowGatewayConditionEditor", () => {
  it("authors the common node-choice branch without JSON", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <WorkflowGatewayConditionEditor
        condition={undefined}
        targetKey="repair"
        targetName="Repair"
        choiceSources={[{ key: "triage", name: "Triage" }]}
        readOnly={false}
        onChange={onChange}
      />,
      { wrapper: Wrapper },
    );

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Decision node" }),
      "triage",
    );

    expect(onChange).toHaveBeenLastCalledWith({
      source: "node_choice",
      node: "triage",
      key: "choice",
      op: "eq",
      value: "repair",
    });
    expect(screen.getByTestId("gateway-condition-preview"))
      .toHaveTextContent("TriagechoosesrepairRepair");
  });

  it("keeps compound conditions in the advanced JSON editor", () => {
    const condition = {
      all: [{
        source: "host_issue",
        key: "priority",
        op: "eq",
        value: "high",
      }],
    };
    render(
      <WorkflowGatewayConditionEditor
        condition={condition}
        targetKey="urgent"
        targetName="Urgent path"
        choiceSources={[]}
        readOnly={false}
        onChange={vi.fn()}
      />,
      { wrapper: Wrapper },
    );

    expect(
      screen.getByRole("textbox", { name: "Structured condition (JSON)" }),
    ).toHaveValue(JSON.stringify(condition, null, 2));
  });

  it("does not clear an advanced condition before a choice source is selected", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <WorkflowGatewayConditionEditor
        condition={{
          source: "host_issue",
          key: "priority",
          op: "eq",
          value: "high",
        }}
        targetKey="urgent"
        targetName="Urgent path"
        choiceSources={[{ key: "triage", name: "Triage" }]}
        readOnly={false}
        onChange={onChange}
      />,
      { wrapper: Wrapper },
    );

    await user.click(screen.getByRole("button", { name: "Node choice" }));
    await user.clear(screen.getByRole("textbox", { name: "Branch value" }));
    await user.type(
      screen.getByRole("textbox", { name: "Branch value" }),
      "repair",
    );

    expect(onChange).not.toHaveBeenCalled();
  });
});
