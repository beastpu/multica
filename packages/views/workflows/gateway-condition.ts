import type {
  WorkflowDefinition,
  WorkflowOutputField,
} from "@multica/core/workflows";

import { workflowPathExists } from "./workflow-graph-editor";

/** A field a gateway condition may read, addressed as `owner.key`. */
export interface GatewayField extends WorkflowOutputField {
  /** Variable namespace: an upstream node key, or "issue" for the host. */
  owner: string;
  /** What the author sees: the node's name, or the issue. */
  ownerLabel: string;
  /** Fully qualified reference used in the expression text. */
  path: string;
}

/** Comparison operators, narrowed per field type. */
export type GatewayOperator =
  | "=="
  | "!="
  | ">"
  | ">="
  | "<"
  | "<="
  | "in"
  | "not in";

export interface GatewayClause {
  path: string;
  operator: GatewayOperator;
  /** Right-hand side. For in/not in this is the list of values. */
  values: string[];
}

/** A condition simple enough to edit as rows rather than as text. */
export interface GatewayConditionForm {
  join: "&&" | "||";
  clauses: GatewayClause[];
}

// The host issue's own fields. Mirrors HostIssueFields() on the server; the
// two are checked against each other by the round-trip test rather than
// shared, because the server list is Go and this one drives a form.
const HOST_ISSUE_FIELDS: WorkflowOutputField[] = [
  { key: "priority", type: "string" },
  { key: "status", type: "string" },
  { key: "title", type: "string" },
  { key: "assignee_type", type: "string" },
  { key: "assignee_id", type: "string" },
  { key: "project_id", type: "string" },
];

// Supplied by the engine for any reviewed activity, so a branch can route on
// what the review concluded without the node declaring anything.
const REVIEWER_FIELDS: WorkflowOutputField[] = [
  { key: "verdict", type: "enum", values: ["pass", "fail", "blocked"] },
  { key: "confidence", type: "number" },
  { key: "reason", type: "string" },
];

export const HOST_ISSUE_OWNER = "issue";

/**
 * Every field this gateway may read: the outputs and verdicts of activities
 * that can reach it, plus the host issue. Restricting to upstream nodes is
 * what stops a condition referencing a sibling branch that may never run.
 */
export function gatewayFieldOptions(
  definition: WorkflowDefinition,
  gatewayKey: string,
  hostIssueLabel: string,
): GatewayField[] {
  const fields: GatewayField[] = [];
  for (const node of definition.nodes) {
    if (node.kind !== "activity") continue;
    if (!workflowPathExists(definition, node.key, gatewayKey)) continue;
    const ownerLabel = node.name || node.key;
    for (const field of node.outputs ?? []) {
      fields.push({
        ...field,
        owner: node.key,
        ownerLabel,
        path: `${node.key}.${field.key}`,
      });
    }
    if (node.reviewer?.kind) {
      for (const field of REVIEWER_FIELDS) {
        fields.push({
          ...field,
          owner: node.key,
          ownerLabel,
          path: `${node.key}.${field.key}`,
        });
      }
    }
  }
  for (const field of HOST_ISSUE_FIELDS) {
    fields.push({
      ...field,
      owner: HOST_ISSUE_OWNER,
      ownerLabel: hostIssueLabel,
      path: `${HOST_ISSUE_OWNER}.${field.key}`,
    });
  }
  return fields;
}

/** Operators that make sense for a field's type. */
export function operatorsForField(field: WorkflowOutputField | undefined) {
  switch (field?.type) {
    case "bool":
      return ["==", "!="] as GatewayOperator[];
    case "number":
      return ["==", "!=", ">", ">=", "<", "<="] as GatewayOperator[];
    case "enum":
      return ["==", "!=", "in", "not in"] as GatewayOperator[];
    default:
      return ["==", "!="] as GatewayOperator[];
  }
}

function quote(field: WorkflowOutputField | undefined, value: string) {
  // bool and number are bare literals; everything else is a quoted string,
  // which is also what the server's parser accepts for those types.
  if (field?.type === "bool" || field?.type === "number") return value;
  return JSON.stringify(value);
}

/** Renders the form back into the expression text that gets stored. */
export function serializeGatewayCondition(
  form: GatewayConditionForm,
  fields: GatewayField[],
): string {
  const byPath = new Map(fields.map((field) => [field.path, field]));
  return form.clauses
    .filter((clause) => clause.path && clause.values.some(Boolean))
    .map((clause) => {
      const field = byPath.get(clause.path);
      if (clause.operator === "in" || clause.operator === "not in") {
        const list = clause.values
          .filter(Boolean)
          .map((value) => quote(field, value))
          .join(", ");
        return `${clause.path} ${clause.operator} [${list}]`;
      }
      return `${clause.path} ${clause.operator} ${quote(field, clause.values[0] ?? "")}`;
    })
    .join(` ${form.join} `);
}

// `==` must not be followed by another `=`: `a === 1` is not this language,
// and matching it as `==` plus a value of `= 1` would quietly accept a typo.
const CLAUSE_PATTERN =
  /^([A-Za-z_][\w.]*)\s*(==(?!=)|!=(?!=)|>=|<=|>|<|not in|in)\s*(.+)$/;

function unquote(raw: string): string[] {
  const text = raw.trim();
  if (text.startsWith("[") && text.endsWith("]")) {
    return text
      .slice(1, -1)
      .split(",")
      .map((part) => unquote(part)[0] ?? "")
      .filter((part) => part.length > 0);
  }
  if (text.startsWith('"') && text.endsWith('"') && text.length >= 2) {
    return [text.slice(1, -1)];
  }
  return [text];
}

/**
 * Reads stored expression text back into form rows, or returns null when it is
 * beyond what rows can express — parentheses, a mix of && and ||, or anything
 * the clause pattern does not recognise. A null means "edit this as text",
 * never a silent reinterpretation: rewriting a condition the author wrote by
 * hand into something subtly different is worse than declining to show it.
 */
export function parseGatewayCondition(
  when: string,
): GatewayConditionForm | null {
  const text = when.trim();
  if (!text || text.includes("(") || text.includes(")") || text.includes("!")) {
    return null;
  }
  const hasAnd = /\s&&\s/.test(text);
  const hasOr = /\s\|\|\s/.test(text);
  if (hasAnd && hasOr) return null;
  const join: "&&" | "||" = hasOr ? "||" : "&&";
  const parts = text.split(hasOr ? /\s\|\|\s/ : /\s&&\s/);

  const clauses: GatewayClause[] = [];
  for (const part of parts) {
    const match = CLAUSE_PATTERN.exec(part.trim());
    if (!match) return null;
    const [, path, operator, rest] = match;
    const values = unquote(rest!);
    if (values.length === 0) return null;
    clauses.push({
      path: path!,
      operator: operator as GatewayOperator,
      values,
    });
  }
  return { join, clauses };
}
