"use client";

import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";
import {
  gatewayFieldOptions,
  operatorsForField,
  parseGatewayCondition,
  serializeGatewayCondition,
  type GatewayClause,
  type GatewayConditionForm,
  type GatewayField,
  type GatewayOperator,
} from "./gateway-condition";
import type { WorkflowDefinition } from "@multica/core/workflows";

const selectClass =
  "min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs";

/**
 * Edits one branch condition as rows of field / operator / value.
 *
 * The stored form stays the expression text, so a condition written by hand
 * keeps working and the server has one thing to parse. A condition the rows
 * cannot represent — parentheses, a mix of && and || — opens in text mode
 * rather than being rewritten into something that reads similar but means
 * something else.
 */
export function GatewayConditionEditor({
  definition,
  gatewayKey,
  when,
  readOnly,
  inputId,
  onChange,
}: {
  definition: WorkflowDefinition;
  gatewayKey: string;
  when: string;
  readOnly: boolean;
  inputId: string;
  onChange: (when: string) => void;
}) {
  const { t } = useT("workflows");
  const fields = gatewayFieldOptions(
    definition,
    gatewayKey,
    t(($) => $.editor.condition_host_issue),
  );
  const parsed = parseGatewayCondition(when);
  // Text mode is sticky once chosen, so toggling to it does not bounce back
  // the moment the text happens to become row-representable again.
  const [textMode, setTextMode] = useState(false);
  // Rows are held here rather than re-derived from the text on every render.
  // A half-filled row — a field chosen, no value yet — serialises to nothing,
  // so deriving would delete the row the moment the author picked a field and
  // leave them nowhere to type the value.
  const [draft, setDraft] = useState<GatewayConditionForm>(
    () => parsed ?? { join: "&&", clauses: [] },
  );
  const emitted = useRef(when);
  useEffect(() => {
    // Only reset from the outside — loading another version, an undo — never
    // from the text this editor just produced.
    if (when === emitted.current) return;
    emitted.current = when;
    setDraft(parseGatewayCondition(when) ?? { join: "&&", clauses: [] });
  }, [when]);

  const rows = draft.clauses;
  const join = draft.join;
  const byPath = new Map(fields.map((field) => [field.path, field]));

  const emit = (nextRows: GatewayClause[], nextJoin: "&&" | "||") => {
    setDraft({ join: nextJoin, clauses: nextRows });
    const text = serializeGatewayCondition(
      { join: nextJoin, clauses: nextRows },
      fields,
    );
    emitted.current = text;
    onChange(text);
  };

  const patch = (index: number, next: Partial<GatewayClause>) => {
    emit(rows.map((row, i) => (i === index ? { ...row, ...next } : row)), join);
  };

  if (textMode || (when.trim() && !parsed)) {
    return (
      <div className="space-y-1.5">
        <Input
          id={inputId}
          value={when}
          disabled={readOnly}
          placeholder={t(($) => $.editor.case_when_placeholder)}
          className="font-mono text-xs"
          onChange={(event) => onChange(event.target.value)}
        />
        <div className="flex items-start justify-between gap-2">
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.editor.case_when_help)}
          </p>
          {!readOnly && parseGatewayCondition(when) && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="shrink-0 text-xs"
              onClick={() => setTextMode(false)}
            >
              {t(($) => $.editor.condition_use_rows)}
            </Button>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {rows.map((row, index) => {
        const field = byPath.get(row.path);
        const operators = operatorsForField(field);
        const isList = row.operator === "in" || row.operator === "not in";
        return (
          <div key={index} className="space-y-1.5 rounded-lg border p-2">
            {index > 0 && (
              <select
                aria-label={t(($) => $.editor.condition_join)}
                value={join}
                disabled={readOnly || index > 1}
                className="min-h-8 rounded-md border border-input bg-background px-2 text-xs"
                onChange={(event) =>
                  emit(rows, event.target.value === "||" ? "||" : "&&")}
              >
                <option value="&&">{t(($) => $.editor.condition_and)}</option>
                <option value="||">{t(($) => $.editor.condition_or)}</option>
              </select>
            )}
            <div className="flex items-start gap-1.5">
              <select
                aria-label={t(($) => $.editor.condition_field)}
                value={row.path}
                disabled={readOnly}
                className={selectClass}
                onChange={(event) => {
                  const next = byPath.get(event.target.value);
                  // The operator and value belong to the old field's type, so
                  // they are reset rather than carried onto a new one.
                  patch(index, {
                    path: event.target.value,
                    operator: operatorsForField(next)[0] ?? "==",
                    values: [],
                  });
                }}
              >
                <option value="">
                  {t(($) => $.editor.condition_choose_field)}
                </option>
                {fields.map((option) => (
                  <option key={option.path} value={option.path}>
                    {option.ownerLabel} · {option.desc || option.key}
                  </option>
                ))}
              </select>
              <select
                aria-label={t(($) => $.editor.condition_operator)}
                value={row.operator}
                disabled={readOnly || !row.path}
                className={`${selectClass} max-w-28`}
                onChange={(event) =>
                  patch(index, {
                    operator: event.target.value as GatewayOperator,
                    values: [],
                  })}
              >
                {operators.map((operator) => (
                  <option key={operator} value={operator}>{operator}</option>
                ))}
              </select>
              {!readOnly && (
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t(($) => $.actions.remove)}
                  onClick={() =>
                    emit(rows.filter((_, i) => i !== index), join)}
                >
                  <Trash2 />
                </Button>
              )}
            </div>
            <ValueInput
              field={field}
              isList={isList}
              values={row.values}
              readOnly={readOnly || !row.path}
              onChange={(values) => patch(index, { values })}
            />
          </div>
        );
      })}
      {!readOnly && (
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="min-h-8 text-xs"
            onClick={() =>
              emit([...rows, { path: "", operator: "==", values: [] }], join)}
          >
            <Plus />
            {t(($) => $.editor.condition_add_row)}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="text-xs"
            onClick={() => setTextMode(true)}
          >
            {t(($) => $.editor.condition_use_text)}
          </Button>
        </div>
      )}
      {rows.length === 0 && (
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.editor.case_when_help)}
        </p>
      )}
    </div>
  );
}

// An enum offers its declared values; a bool offers true/false. Anything else
// is typed, because its range is not knowable from the declaration.
function ValueInput({
  field,
  isList,
  values,
  readOnly,
  onChange,
}: {
  field: GatewayField | undefined;
  isList: boolean;
  values: string[];
  readOnly: boolean;
  onChange: (values: string[]) => void;
}) {
  const { t } = useT("workflows");
  const options = field?.type === "bool"
    ? ["true", "false"]
    : field?.type === "enum"
    ? field.values ?? []
    : null;

  if (options && isList) {
    return (
      <div className="flex flex-wrap gap-2 px-0.5">
        {options.map((option) => (
          <label key={option} className="flex items-center gap-1.5 text-xs">
            <input
              type="checkbox"
              checked={values.includes(option)}
              disabled={readOnly}
              onChange={(event) =>
                onChange(event.target.checked
                  ? [...values, option]
                  : values.filter((value) => value !== option))}
            />
            {option}
          </label>
        ))}
      </div>
    );
  }
  if (options) {
    return (
      <select
        aria-label={t(($) => $.editor.condition_value)}
        value={values[0] ?? ""}
        disabled={readOnly}
        className={selectClass}
        onChange={(event) => onChange([event.target.value])}
      >
        <option value="">{t(($) => $.editor.condition_choose_value)}</option>
        {options.map((option) => (
          <option key={option} value={option}>{option}</option>
        ))}
      </select>
    );
  }
  return (
    <Input
      aria-label={t(($) => $.editor.condition_value)}
      value={values[0] ?? ""}
      disabled={readOnly}
      type={field?.type === "number" ? "number" : "text"}
      className="min-h-9 text-xs"
      onChange={(event) => onChange([event.target.value])}
    />
  );
}
