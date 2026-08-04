export interface NodeChoiceCondition {
  source: "node_choice";
  node: string;
  key: "choice";
  op: "eq";
  value: string;
}

export interface ParsedNodeChoiceCondition {
  nodeKey: string;
  value: string;
}

export function buildNodeChoiceCondition(
  nodeKey: string,
  value: string,
): NodeChoiceCondition {
  return {
    source: "node_choice",
    node: nodeKey,
    key: "choice",
    op: "eq",
    value,
  };
}

export function parseNodeChoiceCondition(
  condition: unknown,
): ParsedNodeChoiceCondition | null {
  if (!condition || typeof condition !== "object" || Array.isArray(condition)) {
    return null;
  }
  const value = condition as Record<string, unknown>;
  if (
    value.source !== "node_choice" ||
    typeof value.node !== "string" ||
    !value.node ||
    value.key !== "choice" ||
    value.op !== "eq" ||
    typeof value.value !== "string" ||
    !value.value
  ) {
    return null;
  }
  return { nodeKey: value.node, value: value.value };
}
