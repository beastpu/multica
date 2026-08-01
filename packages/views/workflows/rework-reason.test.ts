import { describe, expect, it } from "vitest";
import {
  composeReworkReason,
  emptyReworkReasonDraft,
  isReworkReasonComplete,
} from "./rework-reason";

describe("composeReworkReason", () => {
  it("labels each answer so the executor can tell them apart", () => {
    expect(composeReworkReason({
      criterion: "AC-004",
      gap: "~> 5.3 should be blocked but was allowed",
      hint: "pessimistic_upper() treats two-part ~> as < 5.4.0",
    })).toBe([
      "Failed: AC-004",
      "Expected vs actual: ~> 5.3 should be blocked but was allowed",
      "Suggested fix: pessimistic_upper() treats two-part ~> as < 5.4.0",
    ].join("\n"));
  });

  it("omits the answers that were left blank rather than emitting empty labels", () => {
    expect(composeReworkReason({
      criterion: "",
      gap: "the fix did not survive a rerun",
      hint: "   ",
    })).toBe("Expected vs actual: the fix did not survive a rerun");
  });
});

describe("isReworkReasonComplete", () => {
  // The gap is the only field that states what is actually wrong. Letting a
  // rejection through without it reproduces the free-text problem the form
  // exists to fix.
  it("requires the expected-versus-actual answer", () => {
    expect(isReworkReasonComplete(emptyReworkReasonDraft)).toBe(false);
    expect(isReworkReasonComplete({
      criterion: "AC-004",
      gap: "   ",
      hint: "look at the parser",
    })).toBe(false);
    expect(isReworkReasonComplete({
      criterion: "",
      gap: "blocked when it should pass",
      hint: "",
    })).toBe(true);
  });
});
