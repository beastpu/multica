import { describe, expect, it } from "vitest";
import { branchChoiceDuty } from "./branch-choice";

const NODES = [
  { key: "start", kind: "start", name: "开始" },
  { key: "triage", kind: "activity", name: "问题分诊" },
  { key: "route", kind: "gateway", name: "是否需要修复" },
  { key: "fix", kind: "activity", name: "缺陷修复" },
  { key: "end", kind: "end", name: "结束" },
];

const EDGES = [
  { from: "start", to: "triage" },
  { from: "triage", to: "route" },
  {
    from: "route",
    to: "end",
    condition: {
      source: "node_choice",
      node: "triage",
      key: "choice",
      op: "eq",
      value: "end",
    },
  },
  { from: "route", to: "fix", default: true },
];

describe("branchChoiceDuty", () => {
  // Offering every node — the previous behaviour — let an author pick a value
  // no gateway condition matches. The server accepts it and the run then takes
  // the default edge, indistinguishable from never having decided.
  it("offers only the values a gateway condition actually matches", () => {
    const duty = branchChoiceDuty(NODES, EDGES, "triage");
    expect(duty).not.toBeNull();
    expect(duty!.options).toEqual([{ key: "end", target: "结束" }]);
    expect(duty!.gatewayName).toBe("是否需要修复");
    expect(duty!.defaultTarget).toBe("缺陷修复");
  });

  it("reports nothing for a node no gateway branches on", () => {
    expect(branchChoiceDuty(NODES, EDGES, "fix")).toBeNull();
  });

  it("finds choices nested inside all/any groups", () => {
    const duty = branchChoiceDuty(NODES, [
      { from: "triage", to: "route" },
      {
        from: "route",
        to: "end",
        condition: {
          all: [
            { source: "host_issue", key: "priority", op: "eq", value: "low" },
            {
              source: "node_choice",
              node: "triage",
              key: "choice",
              op: "eq",
              value: "end",
            },
          ],
        },
      },
      { from: "route", to: "fix", default: true },
    ], "triage");
    expect(duty!.options).toEqual([{ key: "end", target: "结束" }]);
  });
});
