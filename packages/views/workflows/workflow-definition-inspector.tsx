"use client";

import { Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import {
  workflowCompletionMode,
  type WorkflowArtifactRequirement,
  type WorkflowDefinition,
  type WorkflowExecutorDefinition,
  type WorkflowIssueTemplate,
  type WorkflowNodeAction,
  type WorkflowNodeDefinition,
  type WorkflowOutputField,
  type WorkflowReviewerDefinition,
  type WorkflowRoleDefinition,
} from "@multica/core/workflows";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label as UILabel } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";
import { workflowNodeIsBoundary } from "./workflow-graph-editor";

// The template editor is a dense desktop surface: field labels sit one step
// below body text so option values never outweigh the field they belong to.
function Label({ className, ...props }: React.ComponentProps<typeof UILabel>) {
  return <UILabel className={cn("text-xs", className)} {...props} />;
}

const actorTypes = ["member", "agent", "squad"] as const;
export interface WorkflowActorOption {
  type: "member" | "agent" | "squad";
  id: string;
  name: string;
}

function actorOptionValue(actor: Pick<WorkflowActorOption, "type" | "id">) {
  return `${actor.type}:${actor.id}`;
}

function parseActorOption(value: string) {
  const separator = value.indexOf(":");
  if (separator < 1) return null;
  return {
    type: value.slice(0, separator) as WorkflowActorOption["type"],
    id: value.slice(separator + 1),
  };
}

function stableKey(prefix: string) {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
}

const hostStatuses = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
] as const;

function HostStatusActionSelect({
  label,
  actions,
  readOnly,
  onChange,
}: {
  label: string;
  actions?: WorkflowNodeAction[];
  readOnly: boolean;
  onChange: (actions: WorkflowNodeAction[] | undefined) => void;
}) {
  const current = (actions ?? []).find(
    (action) => action.kind === "set_host_status",
  )?.status ?? "";
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <select
        aria-label={label}
        value={current}
        disabled={readOnly}
        className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
        onChange={(event) => {
          // Preserve action kinds this select does not manage.
          const others = (actions ?? []).filter(
            (action) => action.kind !== "set_host_status",
          );
          const next = event.target.value
            ? [...others, { kind: "set_host_status", status: event.target.value }]
            : others;
          onChange(next.length > 0 ? next : undefined);
        }}
      >
        <option value="">—</option>
        {hostStatuses.map((status) => (
          <option key={status} value={status}>{status}</option>
        ))}
      </select>
    </div>
  );
}

function NodeNeighborList({
  label,
  nodes,
}: {
  label: string;
  nodes: WorkflowNodeDefinition[];
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-xs font-medium">{label}</p>
      {nodes.length === 0 ? (
        <p className="text-xs text-muted-foreground">—</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {nodes.map((item) => (
            <span
              key={item.key}
              className="rounded-md border bg-muted/40 px-2 py-1 text-xs"
            >
              {item.name}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function InspectorSection({
  title,
  children,
  open = false,
}: {
  title: string;
  children: React.ReactNode;
  open?: boolean;
}) {
  return (
    <details open={open} className="group rounded-lg border bg-background">
      <summary className="min-h-9 cursor-pointer select-none px-3 py-2 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {title}
      </summary>
      <div className="space-y-3 border-t p-2.5">{children}</div>
    </details>
  );
}

function RemoveButton({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      size="icon-sm"
      variant="ghost"
      disabled={disabled}
      aria-label={label}
      onClick={onClick}
    >
      <Trash2 />
    </Button>
  );
}

function JsonObjectEditor({
  value,
  readOnly,
  label,
  placeholder,
  onChange,
}: {
  value: unknown;
  readOnly: boolean;
  label: string;
  placeholder: string;
  onChange: (value: unknown | undefined) => void;
}) {
  const { t } = useT("workflows");
  const [text, setText] = useState(
    value === undefined ? "" : JSON.stringify(value, null, 2),
  );
  const [error, setError] = useState("");

  useEffect(() => {
    setText(value === undefined ? "" : JSON.stringify(value, null, 2));
    setError("");
  }, [value]);

  const apply = () => {
    const trimmed = text.trim();
    if (!trimmed) {
      setError("");
      onChange(undefined);
      return;
    }
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error("expected object");
      }
      setError("");
      onChange(parsed);
    } catch {
      setError(t(($) => $.errors.invalid_json));
    }
  };

  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <Textarea
        value={text}
        disabled={readOnly}
        rows={5}
        className="font-mono text-xs"
        placeholder={placeholder}
        onChange={(event) => setText(event.target.value)}
        onBlur={apply}
      />
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

export function WorkflowRoleEditor({
  roles,
  readOnly,
  onChange,
}: {
  roles: WorkflowRoleDefinition[];
  readOnly: boolean;
  onChange: (roles: WorkflowRoleDefinition[]) => void;
}) {
  const { t } = useT("workflows");
  return (
    <div className="space-y-3">
      {roles.map((role, index) => (
        <div key={role.key} className="space-y-3 rounded-lg border p-2.5">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label htmlFor={`workflow-role-name-${role.key}`}>
                {t(($) => $.editor.role_name)}
              </Label>
              <Input
                id={`workflow-role-name-${role.key}`}
                value={role.name}
                disabled={readOnly}
                className="min-h-9 text-xs"
                onChange={(event) => {
                  const next = [...roles];
                  next[index] = { ...role, name: event.target.value };
                  onChange(next);
                }}
              />
              <p className="font-mono text-xs text-muted-foreground">{role.key}</p>
            </div>
            <RemoveButton
              label={t(($) => $.actions.remove)}
              disabled={readOnly}
              onClick={() => onChange(roles.filter((item) => item.key !== role.key))}
            />
          </div>
          <label className="flex min-h-9 items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={role.required}
              disabled={readOnly}
              onChange={(event) => {
                const next = [...roles];
                next[index] = { ...role, required: event.target.checked };
                onChange(next);
              }}
            />
            {t(($) => $.editor.role_required)}
          </label>
          <fieldset>
            <legend className="mb-1.5 text-xs font-medium">
              {t(($) => $.editor.allowed_actor_types)}
            </legend>
            <div className="grid grid-cols-3 gap-1">
              {actorTypes.map((actorType) => (
                <label
                  key={actorType}
                  className="flex min-h-9 items-center gap-1.5 rounded-md border px-2 text-xs"
                >
                  <input
                    type="checkbox"
                    checked={role.allowed_actor_types.includes(actorType)}
                    disabled={readOnly}
                    onChange={(event) => {
                      const values = event.target.checked
                        ? [...role.allowed_actor_types, actorType]
                        : role.allowed_actor_types.filter((item) => item !== actorType);
                      const next = [...roles];
                      next[index] = { ...role, allowed_actor_types: values };
                      onChange(next);
                    }}
                  />
                  {actorType}
                </label>
              ))}
            </div>
          </fieldset>
        </div>
      ))}
      {!readOnly && (
        <Button
          type="button"
          variant="outline"
          className="min-h-9 w-full"
          onClick={() => {
            const key = stableKey("role");
            onChange([
              ...roles,
              { key, name: t(($) => $.editor.new_role), required: false, allowed_actor_types: ["member"] },
            ]);
          }}
        >
          <Plus />
          {t(($) => $.editor.add_role)}
        </Button>
      )}
    </div>
  );
}

function ExecutorEditor({
  node,
  definition,
  actorOptions,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  definition: WorkflowDefinition;
  actorOptions: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const executor = node.executor;
  const fallback = executor?.fallback;

  const setExecutor = (next: WorkflowExecutorDefinition | undefined) =>
    onChange({ ...node, executor: next });

  return (
    <ExecutorEntryFields
      idPrefix={`node-executor-${node.key}`}
      label={t(($) => $.editor.executor)}
      entry={executor}
      definition={definition}
      actorOptions={actorOptions}
      readOnly={readOnly}
      onChange={(entry) => {
        if (!entry) {
          setExecutor(undefined);
          return;
        }
        setExecutor({ ...entry, fallback });
      }}
    />
  );
}

function ExecutorEntryFields({
  idPrefix,
  label,
  hint,
  entry,
  definition,
  actorOptions,
  readOnly,
  onChange,
}: {
  idPrefix: string;
  label: string;
  hint?: string;
  entry: WorkflowExecutorDefinition | undefined;
  definition: WorkflowDefinition;
  actorOptions: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (entry: WorkflowExecutorDefinition | undefined) => void;
}) {
  const { t } = useT("workflows");
  const kinds: Array<NonNullable<WorkflowExecutorDefinition["kind"]>> =
    ["role", "actor", "capability", "manual"];
  const kindLabel = (kind: string) => {
    switch (kind) {
      case "role":
        return t(($) => $.editor.executor_kind_role);
      case "actor":
        return t(($) => $.editor.executor_kind_actor);
      case "capability":
        return t(($) => $.editor.executor_kind_capability);
      case "manual":
        return t(($) => $.editor.executor_kind_manual);
      default:
        return kind;
    }
  };

  return (
    <div className="space-y-1.5">
      <Label htmlFor={`${idPrefix}-kind`}>{label}</Label>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      <select
        id={`${idPrefix}-kind`}
        value={entry?.kind ?? ""}
        disabled={readOnly}
        className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
        onChange={(event) => {
          const kind = event.target.value as WorkflowExecutorDefinition["kind"];
          if (!event.target.value) {
            onChange(undefined);
            return;
          }
          onChange({ kind });
        }}
      >
        <option value="">—</option>
        {kinds.map((kind) => (
          <option key={kind} value={kind}>{kindLabel(kind)}</option>
        ))}
      </select>
      {(entry?.kind === "role" || entry?.kind === "capability") && (
        <select
          aria-label={t(($) => $.editor.executor_role)}
          value={entry.role ?? ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => onChange({
            ...entry,
            role: event.target.value || undefined,
          })}
        >
          <option value="">—</option>
          {definition.roles.map((role) => (
            <option key={role.key} value={role.key}>{role.name}</option>
          ))}
        </select>
      )}
      {entry?.kind === "actor" && (
        <select
          aria-label={t(($) => $.editor.direct_executor)}
          value={entry.actor_type && entry.actor_id
            ? actorOptionValue({ type: entry.actor_type, id: entry.actor_id })
            : ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => {
            const actor = parseActorOption(event.target.value);
            onChange({
              ...entry,
              actor_type: actor?.type,
              actor_id: actor?.id,
            });
          }}
        >
          <option value="">—</option>
          {actorOptions.map((actor) => (
            <option
              key={actorOptionValue(actor)}
              value={actorOptionValue(actor)}
            >
              {actor.name} · {actor.type}
            </option>
          ))}
        </select>
      )}
      {entry?.kind === "capability" && (
        <Input
          aria-label={t(($) => $.editor.capability)}
          value={entry.capability ?? ""}
          disabled={readOnly}
          className="min-h-9 text-xs"
          onChange={(event) => onChange({
            ...entry,
            capability: event.target.value,
          })}
        />
      )}
    </div>
  );
}

// ArtifactEditor declares what a node must deliver.
//
// It sits beside the issue templates because the two are a pair: an issue is
// the work, an artifact is the thing that comes out of it. Both are the node's
// contract, and both were previously only expressible by hand-editing the
// definition JSON — which is why templates in the wild carry no artifacts at
// all and downstream nodes get a handoff summary with nothing behind it.
//
// The key is surfaced rather than hidden because agents submit against it
// (`multica workflow submit --artifact <key>`) and the server rejects any key
// the node did not declare. It is generated once and then left alone: renaming
// it would orphan whatever a running node already submitted.
function ArtifactEditor({
  node,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const artifacts = node.artifacts ?? [];
  const update = (index: number, artifact: WorkflowArtifactRequirement) => {
    const next = [...artifacts];
    next[index] = artifact;
    onChange({ ...node, artifacts: next });
  };

  return (
    <div className="space-y-3">
      {artifacts.map((artifact, index) => (
        <div key={artifact.key} className="space-y-3 rounded-lg border p-2.5">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label>{t(($) => $.editor.artifact_name)}</Label>
              <Input
                value={artifact.name}
                disabled={readOnly}
                className="min-h-9 text-xs"
                onChange={(event) => update(index, {
                  ...artifact,
                  name: event.target.value,
                })}
              />
              <p className="font-mono text-xs text-muted-foreground">
                {artifact.key}
              </p>
            </div>
            <RemoveButton
              label={t(($) => $.actions.remove)}
              disabled={readOnly}
              onClick={() => onChange({
                ...node,
                artifacts: artifacts.filter((item) => item.key !== artifact.key),
              })}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t(($) => $.editor.artifact_description)}</Label>
            <Textarea
              value={artifact.description ?? ""}
              disabled={readOnly}
              rows={2}
              placeholder={t(($) => $.editor.artifact_description_hint)}
              onChange={(event) => update(index, {
                ...artifact,
                description: event.target.value || undefined,
              })}
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.artifact_kind)}</Label>
              <select
                value={artifact.kind ?? "document"}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => update(index, {
                  ...artifact,
                  kind: event.target.value as WorkflowArtifactRequirement["kind"],
                })}
              >
                <option value="document">
                  {t(($) => $.editor.artifact_kind_document)}
                </option>
                <option value="attachment">
                  {t(($) => $.editor.artifact_kind_attachment)}
                </option>
                <option value="link">
                  {t(($) => $.editor.artifact_kind_link)}
                </option>
              </select>
            </div>
            <label className="flex min-h-9 items-center gap-2 self-end text-xs">
              <input
                type="checkbox"
                checked={artifact.required !== false}
                disabled={readOnly}
                onChange={(event) => update(index, {
                  ...artifact,
                  required: event.target.checked,
                })}
              />
              {t(($) => $.editor.artifact_required)}
            </label>
          </div>
        </div>
      ))}
      {!readOnly && (
        <Button
          type="button"
          variant="outline"
          className="min-h-9 w-full"
          onClick={() => onChange({
            ...node,
            artifacts: [
              ...artifacts,
              {
                key: stableKey("artifact"),
                name: "",
                kind: "document",
                required: true,
              },
            ],
          })}
        >
          <Plus />
          {t(($) => $.editor.add_artifact)}
        </Button>
      )}
    </div>
  );
}

// OutputsEditor declares the structured fields a node owes on delivery.
//
// The key is author-editable because it is what gateway conditions reference
// (`is_bug == false`) and what agents submit against (`--set is_bug=false`);
// unlike an artifact key it is meant to be read and typed, so it should carry
// domain meaning rather than a generated suffix.
function OutputsEditor({
  node,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const outputs = node.outputs ?? [];
  const update = (index: number, field: WorkflowOutputField) => {
    const next = [...outputs];
    next[index] = field;
    onChange({ ...node, outputs: next });
  };

  return (
    <div className="space-y-3">
      {outputs.map((field, index) => (
        <div key={index} className="space-y-3 rounded-lg border p-2.5">
          <div className="flex items-start justify-between gap-2">
            <div className="grid min-w-0 flex-1 gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label>{t(($) => $.editor.output_key)}</Label>
                <Input
                  value={field.key}
                  disabled={readOnly}
                  className="min-h-9 font-mono text-xs"
                  placeholder="is_bug"
                  onChange={(event) => update(index, {
                    ...field,
                    key: event.target.value,
                  })}
                />
              </div>
              <div className="space-y-1.5">
                <Label>{t(($) => $.editor.output_type)}</Label>
                <select
                  value={field.type}
                  disabled={readOnly}
                  className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                  onChange={(event) => update(index, {
                    ...field,
                    type: event.target.value as WorkflowOutputField["type"],
                    values: event.target.value === "enum"
                      ? field.values
                      : undefined,
                  })}
                >
                  {(["bool", "enum", "number", "string", "string[]"] as const)
                    .map((kind) => (
                      <option key={kind} value={kind}>{kind}</option>
                    ))}
                </select>
              </div>
            </div>
            <RemoveButton
              label={t(($) => $.actions.remove)}
              disabled={readOnly}
              onClick={() => onChange({
                ...node,
                outputs: outputs.filter((_, i) => i !== index),
              })}
            />
          </div>
          {field.type === "enum" && (
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.output_values)}</Label>
              <Input
                value={(field.values ?? []).join(", ")}
                disabled={readOnly}
                className="min-h-9 font-mono text-xs"
                placeholder="bug, duplicate, works_as_intended"
                onChange={(event) => update(index, {
                  ...field,
                  values: event.target.value
                    .split(",")
                    .map((value) => value.trim())
                    .filter(Boolean),
                })}
              />
            </div>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.output_desc)}</Label>
              <Input
                value={field.desc ?? ""}
                disabled={readOnly}
                className="min-h-9 text-xs"
                onChange={(event) => update(index, {
                  ...field,
                  desc: event.target.value || undefined,
                })}
              />
            </div>
            <label className="flex min-h-9 items-center gap-2 self-end text-xs">
              <input
                type="checkbox"
                checked={field.required === true}
                disabled={readOnly}
                onChange={(event) => update(index, {
                  ...field,
                  required: event.target.checked || undefined,
                })}
              />
              {t(($) => $.editor.output_required)}
            </label>
          </div>
        </div>
      ))}
      {!readOnly && (
        <Button
          type="button"
          variant="outline"
          className="min-h-9 w-full"
          onClick={() => onChange({
            ...node,
            outputs: [...outputs, { key: "", type: "enum" }],
          })}
        >
          <Plus />
          {t(($) => $.editor.add_output)}
        </Button>
      )}
    </div>
  );
}

// Runtime decomposition at the workflow level is retired — see the comment at
// the policy select. These values are still accepted so existing definitions
// keep running; they are simply no longer offered.
function isDeprecatedIssuePolicy(policy: string | undefined): policy is string {
  return policy === "dynamic" || policy === "fixed_and_dynamic";
}

function issuePolicyForEditor(node: WorkflowNodeDefinition): string {
  if (node.issue_policy) return node.issue_policy;
  return (node.issue_templates?.length ?? 0) > 0 ? "fixed" : "none";
}

function IssueTemplateEditor({
  node,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const templates = node.issue_templates ?? [];
  const policy = issuePolicyForEditor(node);
  const canDeclareFixed = policy === "fixed" || policy === "fixed_and_dynamic";
  const update = (index: number, template: WorkflowIssueTemplate) => {
    const next = [...templates];
    next[index] = template;
    onChange({ ...node, issue_templates: next });
  };

  return (
    <div className="space-y-3">
      {templates.map((template, index) => (
        <div key={template.key} className="space-y-3 rounded-lg border p-2.5">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label>{t(($) => $.editor.issue_title)}</Label>
              <Input
                value={template.title}
                disabled={readOnly}
                className="min-h-9 text-xs"
                onChange={(event) => update(index, {
                  ...template,
                  title: event.target.value,
                })}
              />
              <p className="font-mono text-xs text-muted-foreground">{template.key}</p>
            </div>
            <RemoveButton
              label={t(($) => $.actions.remove)}
              disabled={readOnly}
              onClick={() => onChange({
                ...node,
                issue_templates: templates.filter((item) => item.key !== template.key),
              })}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t(($) => $.editor.issue_description)}</Label>
            <Textarea
              value={template.description ?? ""}
              disabled={readOnly}
              rows={2}
              onChange={(event) => update(index, {
                ...template,
                description: event.target.value || undefined,
              })}
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.priority)}</Label>
              <select
                value={template.priority ?? "none"}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => update(index, {
                  ...template,
                  priority: event.target.value,
                })}
              >
                {["none", "low", "medium", "high", "urgent"].map((priority) => (
                  <option key={priority} value={priority}>{priority}</option>
                ))}
              </select>
            </div>
            <label className="flex min-h-9 items-center gap-2 pt-5 text-xs">
              <input
                type="checkbox"
                checked={template.required}
                disabled={readOnly}
                onChange={(event) => update(index, {
                  ...template,
                  required: event.target.checked,
                })}
              />
              {t(($) => $.editor.required_task)}
            </label>
          </div>
        </div>
      ))}
      {!canDeclareFixed && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.editor.fixed_tasks_disabled)}
        </p>
      )}
      {!readOnly && canDeclareFixed && (
        <Button
          type="button"
          variant="outline"
          className="min-h-9 w-full"
          onClick={() => {
            const key = stableKey("task");
            onChange({
              ...node,
              issue_templates: [
                ...templates,
                {
                  key,
                  title: "Complete {{host.title}}",
                  required: true,
                  initial_status: "todo",
                  priority: "none",
                },
              ],
            });
          }}
        >
          <Plus />
          {t(($) => $.editor.add_issue_template)}
        </Button>
      )}
    </div>
  );
}

function CompletionEditor({
  node,
  definition,
  actorOptions,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  definition: WorkflowDefinition;
  actorOptions: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const roles = definition.roles;
  const completion = node.completion ?? {};

  return (
    <div className="space-y-3">
      <ReviewerEditor
        node={node}
        definition={definition}
        actorOptions={actorOptions}
        readOnly={readOnly}
        onChange={onChange}
      />
      {/* Only worth showing where a reviewer exists: the cap bounds how many
          times that reviewer may hand the work back. */}
      {node.reviewer?.kind && (
        <div className="space-y-1.5">
          <Label htmlFor={`max-attempts-${node.key}`}>
            {t(($) => $.editor.max_attempts)}
          </Label>
          <Input
            id={`max-attempts-${node.key}`}
            type="number"
            min={0}
            value={completion.max_attempts ?? ""}
            disabled={readOnly}
            placeholder="0"
            className="min-h-9 text-xs"
            onChange={(event) => {
              const parsed = Number.parseInt(event.target.value, 10);
              onChange({
                ...node,
                completion: {
                  ...completion,
                  max_attempts: Number.isFinite(parsed) && parsed > 0
                    ? parsed
                    : undefined,
                },
              });
            }}
          />
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.editor.max_attempts_hint)}
          </p>
        </div>
      )}
      {roles.length > 0 && (
        <fieldset>
          <legend className="mb-1.5 text-xs font-medium">
            {t(($) => $.editor.completion_authorized_roles)}
          </legend>
          <div className="space-y-1">
            {roles.map((role) => (
              <label
                key={role.key}
                className="flex min-h-9 items-center gap-2 rounded-md border px-2.5 text-xs"
              >
                <input
                  type="checkbox"
                  checked={completion.authorized_roles?.includes(role.key) ?? false}
                  disabled={readOnly}
                  onChange={(event) => {
                    const current = completion.authorized_roles ?? [];
                    const next = event.target.checked
                      ? [...current, role.key]
                      : current.filter((key) => key !== role.key);
                    onChange({
                      ...node,
                      completion: {
                        ...completion,
                        authorized_roles: next.length > 0 ? next : undefined,
                      },
                    });
                  }}
                />
                {role.name}
              </label>
            ))}
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">
            {t(($) => $.editor.completion_authorized_roles_hint)}
          </p>
        </fieldset>
      )}
      {/*
        Which buttons an activity grows is derived, not configured: a reviewer
        means someone passes or sends back, no reviewer means nobody clicks
        anything, and rollback exists either way. Authors could not see that
        from a reviewer select and a roles checklist, so rollback in particular
        read as missing.
      */}
      <div className="rounded-lg border bg-muted/20 p-2.5">
        <p className="mb-1.5 text-xs font-medium">
          {t(($) => $.editor.runtime_buttons)}
        </p>
        <ul className="space-y-1 text-xs text-muted-foreground">
          <li>
            {reviewerForEditor(node)
              ? t(($) => $.editor.runtime_button_review)
              : t(($) => $.editor.runtime_button_auto)}
          </li>
          <li>{t(($) => $.editor.runtime_button_rollback)}</li>
        </ul>
      </div>
    </div>
  );
}

// ReviewerEditor is the whole of "who checks this". It replaced a verdict
// evaluator select, a required-verdict select, and a six-value confirmation
// select that could disagree with each other.
function reviewerForEditor(
  node: WorkflowNodeDefinition,
): WorkflowReviewerDefinition | undefined {
  if (node.reviewer) return node.reviewer;
  if (workflowCompletionMode(node) !== "manual") return undefined;
  if (node.owner_role) {
    return { kind: "role", role: node.owner_role, required: true };
  }
  if (node.executor?.kind === "actor" && node.executor.actor_type === "member") {
    return {
      kind: "actor",
      actor_type: "member",
      actor_id: node.executor.actor_id,
      required: true,
    };
  }
  return undefined;
}

function ReviewerEditor({
  node,
  definition,
  actorOptions,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  definition: WorkflowDefinition;
  actorOptions: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const reviewer = reviewerForEditor(node);
  const reviewerMode = reviewer?.kind === "actor"
    ? (reviewer.actor_type === "member" ? "human" : "agent")
    : (reviewer?.kind ?? "");
  const setReviewer = (next: WorkflowReviewerDefinition | undefined) =>
    onChange({
      ...node,
      reviewer: next,
      completion: { ...(node.completion ?? {}), mode: "automatic" },
    });

  return (
    <div className="space-y-2">
      <Label htmlFor={`node-reviewer-${node.key}`}>
        {t(($) => $.editor.reviewer)}
      </Label>
      <p className="text-xs text-muted-foreground">
        {t(($) => $.editor.reviewer_hint)}
      </p>
      <select
        id={`node-reviewer-${node.key}`}
        value={reviewerMode}
        disabled={readOnly}
        className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
        onChange={(event) => {
          if (!event.target.value) {
            setReviewer(undefined);
            return;
          }
          const mode = event.target.value;
          if (mode === "human" || mode === "agent") {
            setReviewer({
              kind: "actor",
              actor_type: mode === "human" ? "member" : "agent",
              required: true,
            });
            return;
          }
          setReviewer({
            kind: mode as WorkflowReviewerDefinition["kind"],
            required: true,
          });
        }}
      >
        <option value="">{t(($) => $.editor.reviewer_kind_none)}</option>
        <option value="human">{t(($) => $.editor.reviewer_kind_human)}</option>
        <option value="agent">{t(($) => $.editor.reviewer_kind_agent)}</option>
        <option value="role">{t(($) => $.editor.reviewer_kind_role)}</option>
        {reviewerMode === "owner" && (
          <option value="owner">{t(($) => $.editor.reviewer_kind_owner)}</option>
        )}
        <optgroup label={t(($) => $.editor.reviewer_advanced)}>
          <option value="api">{t(($) => $.editor.reviewer_kind_api)}</option>
          <option value="auto">{t(($) => $.editor.reviewer_kind_auto)}</option>
        </optgroup>
      </select>
      {reviewerMode === "agent" && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.editor.reviewer_agent_protocol_hint)}
        </p>
      )}
      {reviewerMode === "human" && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.editor.reviewer_human_hint)}
        </p>
      )}
      {reviewer?.kind === "role" && (
        <select
          aria-label={t(($) => $.editor.reviewer_kind_role)}
          value={reviewer.role ?? ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => setReviewer({
            ...reviewer,
            role: event.target.value || undefined,
          })}
        >
          <option value="">—</option>
          {definition.roles.map((role) => (
            <option key={role.key} value={role.key}>{role.name}</option>
          ))}
        </select>
      )}
      {reviewer?.kind === "actor" && (
        <select
          aria-label={reviewerMode === "human"
            ? t(($) => $.editor.reviewer_kind_human)
            : t(($) => $.editor.reviewer_kind_agent)}
          value={reviewer.actor_type && reviewer.actor_id
            ? actorOptionValue({ type: reviewer.actor_type, id: reviewer.actor_id })
            : ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => {
            const actor = parseActorOption(event.target.value);
            setReviewer({
              ...reviewer,
              actor_type: actor?.type,
              actor_id: actor?.id,
            });
          }}
        >
          <option value="">—</option>
          {actorOptions
            .filter((actor) => reviewerMode === "human"
              ? actor.type === "member"
              : actor.type === "agent" || actor.type === "squad")
            .map((actor) => (
              <option
                key={actorOptionValue(actor)}
                value={actorOptionValue(actor)}
              >
                {actor.name} · {actor.type}
              </option>
            ))}
        </select>
      )}
      {reviewer?.kind === "api" && (
        <div className="space-y-1.5">
          <Label htmlFor={`node-reviewer-url-${node.key}`}>
            {t(($) => $.editor.reviewer_api_url)}
          </Label>
          <Input
            id={`node-reviewer-url-${node.key}`}
            value={reviewer.api_url ?? ""}
            disabled={readOnly}
            className="min-h-9 text-xs"
            placeholder="https://ci.example.com/verdict"
            onChange={(event) => setReviewer({
              ...reviewer,
              api_url: event.target.value || undefined,
            })}
          />
        </div>
      )}
      {reviewer?.kind === "auto" && (
        <JsonObjectEditor
          label={t(($) => $.editor.verdict_condition)}
          value={reviewer.condition}
          readOnly={readOnly}
          placeholder={'{"source":"node_submission","node":"review","key":"approved","op":"eq","value":true}'}
          onChange={(condition) => setReviewer({ ...reviewer, condition })}
        />
      )}
    </div>
  );
}

// The node's identity and the one action that applies to the node as a whole.
// Deleting used to sit at the very bottom of the panel as a full-width red
// button, which meant scrolling past every field to reach it and reading a
// destructive control as the panel's conclusion. It belongs beside the name it
// destroys.
function NodeInspectorHeader({
  node,
  onRemove,
}: {
  node: WorkflowNodeDefinition;
  onRemove?: () => void;
}) {
  const { t } = useT("workflows");
  return (
    <div className="flex items-start justify-between gap-2">
      <div className="min-w-0">
        <h2 className="truncate text-sm font-medium">{node.name}</h2>
        <p className="mt-1 truncate font-mono text-xs text-muted-foreground">
          {node.key}
        </p>
      </div>
      {onRemove && (
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          className="shrink-0 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          aria-label={t(($) => $.editor.remove_node)}
          onClick={onRemove}
        >
          <Trash2 />
        </Button>
      )}
    </div>
  );
}

export function WorkflowNodeDefinitionInspector({
  node,
  definition,
  actorOptions = [],
  readOnly,
  onChange,
  onRemove,
}: {
  node: WorkflowNodeDefinition;
  definition: WorkflowDefinition;
  actorOptions?: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
  onRemove?: () => void;
}) {
  const { t } = useT("workflows");
  const activity = node.kind === "activity";
  const issuePolicy = issuePolicyForEditor(node);

  // Start and End are structural anchors. Their identity and behavior come
  // from the graph, so rendering ordinary node fields suggests configuration
  // that the author should never need to make.
  if (workflowNodeIsBoundary(node)) {
    return <NodeInspectorHeader node={node} />;
  }

  const basicFields = (
    <>
      <div className="space-y-1.5">
        <Label htmlFor="workflow-node-name">{t(($) => $.editor.activity_name)}</Label>
        <Input
          id="workflow-node-name"
          value={node.name}
          disabled={readOnly}
          className="min-h-9 text-xs"
          onChange={(event) => onChange({ ...node, name: event.target.value })}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="workflow-node-description">
          {t(($) => $.editor.node_description)}
        </Label>
        <Textarea
          id="workflow-node-description"
          value={node.description ?? ""}
          disabled={readOnly}
          rows={2}
          onChange={(event) => onChange({
            ...node,
            description: event.target.value || undefined,
          })}
        />
      </div>
      {activity && (
        <div className="space-y-1.5">
          <Label htmlFor="workflow-node-timeout">{t(($) => $.editor.timeout_minutes)}</Label>
          <Input
            id="workflow-node-timeout"
            type="number"
            min={0}
            max={525600}
            value={node.timeout_minutes ?? 0}
            disabled={readOnly}
            className="min-h-9 text-xs"
            onChange={(event) => onChange({
              ...node,
              timeout_minutes: Number(event.target.value) || undefined,
            })}
          />
          <p className="text-xs text-muted-foreground">
            {t(($) => $.editor.timeout_minutes_hint)}
          </p>
        </div>
      )}
      {node.kind === "parallel_join" && (
        <div className="space-y-1.5">
          <Label>{t(($) => $.editor.join_mode)}</Label>
          <select
            value={node.join_mode ?? "all"}
            disabled={readOnly}
            className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
            onChange={(event) => onChange({ ...node, join_mode: event.target.value })}
          >
            <option value="all">{t(($) => $.editor.join_all)}</option>
            <option value="any">{t(($) => $.editor.join_any)}</option>
          </select>
        </div>
      )}
    </>
  );

  if (!activity) {
    return (
      <div className="space-y-3">
        <NodeInspectorHeader node={node} onRemove={onRemove} />
        <InspectorSection title={t(($) => $.editor.section_basic)} open>
          {basicFields}
        </InspectorSection>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <NodeInspectorHeader node={node} onRemove={onRemove} />
      <Tabs key={node.key} defaultValue="info" className="gap-3">
        <TabsList variant="line" className="w-full justify-start">
          <TabsTrigger value="info">{t(($) => $.editor.tab_info)}</TabsTrigger>
          <TabsTrigger value="work">{t(($) => $.editor.tab_work)}</TabsTrigger>
          <TabsTrigger value="transition">
            {t(($) => $.editor.tab_transition)}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="info" className="space-y-3">
          {basicFields}
          <div className="space-y-3 border-t pt-3">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t(($) => $.editor.section_responsibility)}
            </p>
            <ExecutorEditor
              key={node.key}
              node={node}
              definition={definition}
              actorOptions={actorOptions}
              readOnly={readOnly}
              onChange={onChange}
            />
          </div>
          <div className="space-y-3 border-t pt-3">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t(($) => $.editor.section_flow)}
            </p>
            <NodeNeighborList
              label={t(($) => $.editor.flow_predecessors)}
              nodes={definition.nodes.filter((item) =>
                definition.edges.some(
                  (edge) => edge.to === node.key && edge.from === item.key,
                ),
              )}
            />
            <NodeNeighborList
              label={t(($) => $.editor.flow_successors)}
              nodes={definition.nodes.filter((item) =>
                definition.edges.some(
                  (edge) => edge.from === node.key && edge.to === item.key,
                ),
              )}
            />
          </div>
        </TabsContent>
        <TabsContent value="work" className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="workflow-node-issue-policy">
              {t(($) => $.editor.issue_policy)}
            </Label>
            <select
              id="workflow-node-issue-policy"
              value={issuePolicy}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => {
                const policy = event.target.value;
                // Auto declares no template of its own, so it drops any the
                // author had — but it is still issue-backed, and its node
                // waits for that issue like any other.
                const withoutFixedIssues = policy === "none" ||
                  policy === "auto" || policy === "dynamic";
                const withoutIssues = policy === "none" || policy === "dynamic";
                const requiredIssueOutcome = withoutIssues
                  ? "none"
                  : node.completion?.required_issue_outcome === "none"
                  ? "done"
                  : node.completion?.required_issue_outcome ?? "done";
                onChange({
                  ...node,
                  issue_policy: policy,
                  issue_templates: withoutFixedIssues
                    ? []
                    : node.issue_templates,
                  completion: {
                    ...node.completion,
                    required_issue_outcome: requiredIssueOutcome,
                  },
                });
              }}
            >
              <option value="auto">{t(($) => $.editor.issue_policy_auto)}</option>
              <option value="none">{t(($) => $.editor.issue_policy_none)}</option>
              <option value="fixed">{t(($) => $.editor.issue_policy_fixed)}</option>
              {/*
                Runtime decomposition is not offered any more. Breaking work
                down already happens one level below, as sub-issues under the
                activity's own issue — that is what squads do, and the stage
                barrier already reports when they are all finished. A parallel
                decomposition at the workflow level was the same thing recorded
                twice, and it was never used once: every task ever materialised
                came from a template.
                Existing definitions keep working, and a node still carrying an
                old policy shows it so the value is legible rather than silently
                rewritten.
              */}
              {isDeprecatedIssuePolicy(node.issue_policy) && (
                <option value={node.issue_policy}>
                  {node.issue_policy === "dynamic"
                    ? t(($) => $.editor.issue_policy_dynamic)
                    : t(($) => $.editor.issue_policy_both)}
                  {" · "}
                  {t(($) => $.editor.issue_policy_deprecated)}
                </option>
              )}
            </select>
            {isDeprecatedIssuePolicy(node.issue_policy) && (
              <p className="text-xs text-amber-700 dark:text-amber-300">
                {t(($) => $.editor.issue_policy_deprecated_hint)}
              </p>
            )}
          </div>
          {/* Auto names its own issue, so there is no template to fill in.
              Showing an empty title and description under it would read as
              two more required fields before the node can do anything. */}
          {issuePolicy !== "none" && issuePolicy !== "auto" && (
            <IssueTemplateEditor
              node={node}
              readOnly={readOnly}
              onChange={onChange}
            />
          )}
          {issuePolicy === "auto" && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.issue_policy_auto_hint)}
            </p>
          )}
          {/* Artifacts are independent of issue generation. A run-only node
              can still owe the following nodes a formal deliverable. */}
          <div className="space-y-1.5 border-t pt-3">
            <Label>{t(($) => $.editor.artifacts)}</Label>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.artifacts_hint)}
            </p>
          </div>
          <ArtifactEditor
            node={node}
            readOnly={readOnly}
            onChange={onChange}
          />
          {/* Output fields are the facts a gateway can route on. Declared
              beside artifacts because both are halves of the node's delivery
              contract: the artifact is the work, the outputs are the verdict
              about it. */}
          <div className="space-y-1.5 border-t pt-3">
            <Label>{t(($) => $.editor.outputs)}</Label>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.outputs_hint)}
            </p>
          </div>
          <OutputsEditor
            node={node}
            readOnly={readOnly}
            onChange={onChange}
          />
        </TabsContent>
        <TabsContent value="transition" className="space-y-3">
          <CompletionEditor
            node={node}
            definition={definition}
            actorOptions={actorOptions}
            readOnly={readOnly}
            onChange={onChange}
          />
          <div className="space-y-3 border-t pt-3">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t(($) => $.editor.section_node_events)}
            </p>
            <HostStatusActionSelect
              label={t(($) => $.editor.on_enter_host_status)}
              actions={node.on_enter}
              readOnly={readOnly}
              onChange={(actions) => onChange({ ...node, on_enter: actions })}
            />
            <HostStatusActionSelect
              label={t(($) => $.editor.on_complete_host_status)}
              actions={node.on_complete}
              readOnly={readOnly}
              onChange={(actions) => onChange({ ...node, on_complete: actions })}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.node_events_hint)}
            </p>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
