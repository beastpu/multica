"use client";

import { ArrowRight, Braces, GitFork } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import {
  buildNodeChoiceCondition,
  parseNodeChoiceCondition,
} from "./workflow-gateway-condition";

interface ChoiceSource {
  key: string;
  name: string;
}

interface WorkflowGatewayConditionEditorProps {
  condition: unknown;
  targetKey: string;
  targetName: string;
  choiceSources: ChoiceSource[];
  readOnly: boolean;
  onChange: (condition: unknown | undefined) => void;
}

type ConditionEditorMode = "choice" | "advanced";

export function WorkflowGatewayConditionEditor({
  condition,
  targetKey,
  targetName,
  choiceSources,
  readOnly,
  onChange,
}: WorkflowGatewayConditionEditorProps) {
  const { t } = useT("workflows");
  const id = useId();
  const parsed = parseNodeChoiceCondition(condition);
  const [mode, setMode] = useState<ConditionEditorMode>(
    condition && !parsed ? "advanced" : "choice",
  );
  const [choiceNode, setChoiceNode] = useState(parsed?.nodeKey ?? "");
  const [choiceValue, setChoiceValue] = useState(parsed?.value ?? targetKey);
  const [conditionText, setConditionText] = useState(
    condition ? JSON.stringify(condition, null, 2) : "",
  );
  const [conditionError, setConditionError] = useState("");

  useEffect(() => {
    const nextParsed = parseNodeChoiceCondition(condition);
    setMode(condition && !nextParsed ? "advanced" : "choice");
    setChoiceNode(nextParsed?.nodeKey ?? "");
    setChoiceValue(nextParsed?.value ?? targetKey);
    setConditionText(condition ? JSON.stringify(condition, null, 2) : "");
    setConditionError("");
  }, [condition, targetKey]);

  const updateChoice = (nodeKey: string, value: string) => {
    // Choosing the friendly editor is not itself a destructive edit. Keep an
    // existing advanced condition until the user has selected a source node.
    if (!nodeKey) return;
    if (!value.trim()) {
      onChange(undefined);
      return;
    }
    onChange(buildNodeChoiceCondition(nodeKey, value.trim()));
  };

  const applyAdvancedCondition = () => {
    const value = conditionText.trim();
    if (!value) {
      onChange(undefined);
      setConditionError("");
      return;
    }
    try {
      const next = JSON.parse(value) as unknown;
      if (!next || typeof next !== "object" || Array.isArray(next)) {
        throw new Error("condition must be an object");
      }
      onChange(next);
      setConditionError("");
    } catch {
      setConditionError(t(($) => $.errors.invalid_json));
    }
  };

  const selectedSource = choiceSources.find(
    (source) => source.key === choiceNode,
  );

  return (
    <div className="space-y-3">
      <div
        role="group"
        aria-label={t(($) => $.editor.condition_type)}
        className="grid grid-cols-2 rounded-lg bg-muted/70 p-1"
      >
        <button
          type="button"
          aria-pressed={mode === "choice"}
          disabled={readOnly}
          onClick={() => setMode("choice")}
          className={cn(
            "flex min-h-9 items-center justify-center gap-1.5 rounded-md px-2 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
            mode === "choice"
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          <GitFork aria-hidden="true" className="size-3.5" />
          {t(($) => $.editor.condition_node_choice)}
        </button>
        <button
          type="button"
          aria-pressed={mode === "advanced"}
          disabled={readOnly}
          onClick={() => setMode("advanced")}
          className={cn(
            "flex min-h-9 items-center justify-center gap-1.5 rounded-md px-2 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
            mode === "advanced"
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          <Braces aria-hidden="true" className="size-3.5" />
          {t(($) => $.editor.condition_advanced)}
        </button>
      </div>

      {mode === "choice" ? (
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-source`}>
              {t(($) => $.editor.condition_decision_node)}
            </Label>
            <select
              id={`${id}-source`}
              value={choiceNode}
              disabled={readOnly}
              onChange={(event) => {
                const nodeKey = event.target.value;
                setChoiceNode(nodeKey);
                updateChoice(nodeKey, choiceValue || targetKey);
              }}
              className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <option value="">
                {t(($) => $.editor.condition_choose_node)}
              </option>
              {choiceSources.map((source) => (
                <option key={source.key} value={source.key}>
                  {source.name}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-value`}>
              {t(($) => $.editor.condition_branch_value)}
            </Label>
            <Input
              id={`${id}-value`}
              value={choiceValue}
              disabled={readOnly}
              onChange={(event) => {
                const value = event.target.value;
                setChoiceValue(value);
                updateChoice(choiceNode, value);
              }}
              placeholder={targetKey}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.condition_branch_value_help)}
            </p>
          </div>
          {selectedSource && choiceValue.trim() && (
            <div
              data-testid="gateway-condition-preview"
              className="flex items-center gap-2 rounded-md border border-brand/20 bg-brand/5 px-2.5 py-2 text-xs text-foreground"
            >
              <span className="truncate font-medium">{selectedSource.name}</span>
              <span className="text-muted-foreground">
                {t(($) => $.editor.condition_chooses)}
              </span>
              <code className="max-w-28 truncate rounded bg-background px-1.5 py-0.5 text-[11px]">
                {choiceValue.trim()}
              </code>
              <ArrowRight aria-hidden="true" className="size-3.5 shrink-0 text-brand" />
              <span className="truncate font-medium">{targetName}</span>
            </div>
          )}
          {choiceSources.length === 0 && (
            <p className="rounded-md border border-dashed px-2.5 py-2 text-xs text-muted-foreground">
              {t(($) => $.editor.condition_no_upstream_nodes)}
            </p>
          )}
        </div>
      ) : (
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-json`}>
            {t(($) => $.editor.edge_condition)}
          </Label>
          <Textarea
            id={`${id}-json`}
            value={conditionText}
            disabled={readOnly}
            onChange={(event) => setConditionText(event.target.value)}
            onBlur={applyAdvancedCondition}
            rows={7}
            className="font-mono text-xs"
            placeholder={'{"source":"node_choice","node":"triage","key":"choice","op":"eq","value":"repair"}'}
          />
          <p className="text-xs text-muted-foreground">
            {t(($) => $.editor.condition_advanced_help)}
          </p>
          {conditionError && (
            <p role="alert" className="text-xs text-destructive">
              {conditionError}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
