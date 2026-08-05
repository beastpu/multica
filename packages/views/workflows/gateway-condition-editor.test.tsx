// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { WorkflowDefinition } from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { GatewayConditionEditor } from "./gateway-condition-editor";

const definition: WorkflowDefinition = {
  schema_version: 1,
  name: "Fix",
  roles: [],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    {
      key: "triage",
      kind: "activity",
      name: "Triage",
      outputs: [
        { key: "is_bug", type: "bool", desc: "Real defect" },
        { key: "category", type: "enum", values: ["bug", "duplicate"] },
      ],
    },
    { key: "route", kind: "gateway", name: "Route" },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "triage" },
    { from: "triage", to: "route" },
    { from: "route", to: "end" },
  ],
  acceptance: {},
};

// The editor is controlled, so the harness holds the value the way the real
// page does — otherwise an edit never comes back and nothing re-renders.
function Harness({
  initial,
  onChange,
}: {
  initial: string;
  onChange: (when: string) => void;
}) {
  const [when, setWhen] = useState(initial);
  return (
    <GatewayConditionEditor
      definition={definition}
      gatewayKey="route"
      when={when}
      readOnly={false}
      inputId="case-when"
      onChange={(next) => {
        setWhen(next);
        onChange(next);
      }}
    />
  );
}

function renderEditor(when: string, onChange = vi.fn()) {
  render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      <Harness initial={when} onChange={onChange} />
    </I18nProvider>,
  );
  return onChange;
}

describe("GatewayConditionEditor", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows a saved condition as populated rows", () => {
    renderEditor(`triage.is_bug == false`);

    expect((screen.getByLabelText("Field") as HTMLSelectElement).value)
      .toBe("triage.is_bug");
    expect((screen.getByLabelText("Operator") as HTMLSelectElement).value)
      .toBe("==");
    expect((screen.getByLabelText("Value") as HTMLSelectElement).value)
      .toBe("false");
  });

  it("offers only the operators the field's type supports", async () => {
    const user = userEvent.setup();
    renderEditor(`triage.is_bug == false`);

    const operators = () =>
      [...(screen.getByLabelText("Operator") as HTMLSelectElement).options]
        .map((option) => option.value);
    // A bool compares for equality and nothing else.
    expect(operators()).toEqual(["==", "!="]);

    await user.selectOptions(screen.getByLabelText("Field"), "triage.category");
    // An enum adds membership but still no ordering.
    expect(operators()).toContain("in");
    expect(operators()).not.toContain(">");
  });

  it("writes the expression back out when a value changes", async () => {
    const user = userEvent.setup();
    const onChange = renderEditor(`triage.category == "bug"`);

    await user.selectOptions(screen.getByLabelText("Value"), "duplicate");

    expect(onChange).toHaveBeenCalledWith(`triage.category == "duplicate"`);
  });

  // Rewriting it into something the rows can hold would change its meaning,
  // so the editor hands it back as text instead.
  it("falls back to text for a condition rows cannot express", () => {
    renderEditor(`(triage.is_bug == false || triage.category == "bug") && x == 1`);

    expect(screen.queryByLabelText("Field")).toBeNull();
    expect((screen.getByRole("textbox") as HTMLInputElement).value)
      .toContain("||");
  });
});
