import type { WorkflowDefinition } from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  gatewayFieldOptions,
  operatorsForField,
  parseGatewayCondition,
  serializeGatewayCondition,
} from "./gateway-condition";

function definition(): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "Fix",
    roles: [],
    nodes: [
      { key: "start", kind: "start", name: "开始" },
      {
        key: "triage",
        kind: "activity",
        name: "问题分诊",
        outputs: [
          { key: "is_bug", type: "bool", required: true, desc: "是否为真实缺陷" },
          { key: "category", type: "enum", values: ["bug", "duplicate"] },
        ],
      },
      {
        key: "review",
        kind: "activity",
        name: "代码评审",
        reviewer: { kind: "role", role: "owner" },
      },
      // Not upstream of the gateway: a sibling that may never run.
      {
        key: "sibling",
        kind: "activity",
        name: "旁支",
        outputs: [{ key: "unrelated", type: "bool" }],
      },
      { key: "route", kind: "gateway", name: "Route" },
      { key: "end", kind: "end", name: "End" },
    ],
    edges: [
      { from: "start", to: "triage" },
      { from: "triage", to: "review" },
      { from: "review", to: "route" },
      { from: "start", to: "sibling" },
      { from: "route", to: "end" },
    ],
    acceptance: {},
  };
}

describe("gatewayFieldOptions", () => {
  it("offers upstream outputs, reviewer verdicts and the host issue", () => {
    const paths = gatewayFieldOptions(definition(), "route", "需求").map(
      (field) => field.path,
    );

    expect(paths).toContain("triage.is_bug");
    expect(paths).toContain("triage.category");
    // Supplied by the engine for a reviewed node — nothing declares them.
    expect(paths).toContain("review.verdict");
    expect(paths).toContain("review.confidence");
    expect(paths).toContain("issue.priority");
    // A sibling branch is not upstream, so routing on it is not offered.
    expect(paths).not.toContain("sibling.unrelated");
  });

  it("labels a field by its node so two nodes' fields stay distinguishable", () => {
    const fields = gatewayFieldOptions(definition(), "route", "需求");
    const verdict = fields.find((field) => field.path === "review.verdict");
    expect(verdict?.ownerLabel).toBe("代码评审");
    expect(fields.find((f) => f.path === "issue.priority")?.ownerLabel)
      .toBe("需求");
  });
});

describe("operatorsForField", () => {
  it("narrows operators to what the type supports", () => {
    expect(operatorsForField({ key: "a", type: "bool" })).toEqual(["==", "!="]);
    expect(operatorsForField({ key: "a", type: "number" }))
      .toContain(">=");
    // Ordering comparisons are meaningless on an enum; membership is not.
    const enumOps = operatorsForField({ key: "a", type: "enum", values: ["x"] });
    expect(enumOps).toContain("in");
    expect(enumOps).not.toContain(">");
  });
});

describe("gateway condition round trip", () => {
  const fields = gatewayFieldOptions(definition(), "route", "需求");

  it("writes bare literals for bool and number, quotes the rest", () => {
    expect(serializeGatewayCondition({
      join: "&&",
      clauses: [{ path: "triage.is_bug", operator: "==", values: ["false"] }],
    }, fields)).toBe(`triage.is_bug == false`);

    expect(serializeGatewayCondition({
      join: "&&",
      clauses: [{ path: "review.confidence", operator: "<", values: ["0.7"] }],
    }, fields)).toBe(`review.confidence < 0.7`);

    expect(serializeGatewayCondition({
      join: "&&",
      clauses: [{ path: "issue.priority", operator: "==", values: ["urgent"] }],
    }, fields)).toBe(`issue.priority == "urgent"`);
  });

  it("survives a round trip through text", () => {
    const form = {
      join: "&&" as const,
      clauses: [
        { path: "triage.is_bug", operator: "==" as const, values: ["false"] },
        {
          path: "triage.category",
          operator: "in" as const,
          values: ["bug", "duplicate"],
        },
      ],
    };
    const text = serializeGatewayCondition(form, fields);
    expect(text).toBe(
      `triage.is_bug == false && triage.category in ["bug", "duplicate"]`,
    );
    expect(parseGatewayCondition(text)).toEqual(form);
  });

  it("reads an || condition back as one", () => {
    const text = `issue.priority == "urgent" || triage.is_bug == true`;
    expect(parseGatewayCondition(text)?.join).toBe("||");
    expect(parseGatewayCondition(text)?.clauses).toHaveLength(2);
  });

  // Declining is the point: silently reshaping a hand-written condition into
  // something the rows can hold would change what it means.
  it("declines conditions the rows cannot express", () => {
    for (const text of [
      `(a == 1 || b == 2) && c == 3`,
      `a == 1 && b == 2 || c == 3`,
      `!(a == 1)`,
      `a === 1`,
      ``,
    ]) {
      expect(parseGatewayCondition(text)).toBeNull();
    }
  });

  it("drops empty rows rather than emitting a broken expression", () => {
    expect(serializeGatewayCondition({
      join: "&&",
      clauses: [
        { path: "triage.is_bug", operator: "==", values: ["true"] },
        { path: "", operator: "==", values: [] },
        { path: "triage.category", operator: "==", values: [""] },
      ],
    }, fields)).toBe(`triage.is_bug == true`);
  });

  // Shorthand is what the server accepts and what an author is likely to have
  // typed. The rows address fields fully, so it is expanded rather than shown
  // as an unselected dropdown that looks like the condition was lost.
  it("expands an unqualified field to the node that declares it", () => {
    const form = parseGatewayCondition(`is_bug == false`, fields);
    expect(form?.clauses[0]?.path).toBe("triage.is_bug");
    expect(serializeGatewayCondition(form!, fields)).toBe(
      `triage.is_bug == false`,
    );
  });

  it("stays in text mode when the shorthand is ambiguous or unknown", () => {
    // Two nodes both declaring `done` cannot be resolved to one of them.
    const ambiguous = [
      ...fields,
      { key: "is_bug", type: "bool" as const, owner: "verify",
        ownerLabel: "复核", path: "verify.is_bug" },
    ];
    expect(parseGatewayCondition(`is_bug == false`, ambiguous)).toBeNull();
    expect(parseGatewayCondition(`nothing_declares_this == 1`, fields)).toBeNull();
  });
});
