import { describe, expect, it } from "vitest";
import {
  buildNodeChoiceCondition,
  parseNodeChoiceCondition,
} from "./workflow-gateway-condition";

describe("workflow gateway node-choice conditions", () => {
  it("round-trips the common branch condition", () => {
    const condition = buildNodeChoiceCondition("triage", "repair");

    expect(condition).toEqual({
      source: "node_choice",
      node: "triage",
      key: "choice",
      op: "eq",
      value: "repair",
    });
    expect(parseNodeChoiceCondition(condition)).toEqual({
      nodeKey: "triage",
      value: "repair",
    });
  });

  it("leaves compound and non-choice conditions to the advanced editor", () => {
    expect(parseNodeChoiceCondition({
      all: [{
        source: "node_choice",
        node: "triage",
        key: "choice",
        op: "eq",
        value: "repair",
      }],
    })).toBeNull();
    expect(parseNodeChoiceCondition({
      source: "host_issue",
      key: "priority",
      op: "eq",
      value: "high",
    })).toBeNull();
  });
});
