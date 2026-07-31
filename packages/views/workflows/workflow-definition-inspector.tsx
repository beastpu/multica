"use client";

import { Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import type {
  WorkflowArtifactRequirement,
  WorkflowDefinition,
  WorkflowExecutorStrategy,
  WorkflowIssueTemplate,
  WorkflowNodeAction,
  WorkflowNodeDefinition,
  WorkflowRoleDefinition,
} from "@multica/core/workflows";
import { workflowCompletionMode } from "@multica/core/workflows";
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

// The template editor is a dense desktop surface: field labels sit one step
// below body text so option values never outweigh the field they belong to.
function Label({ className, ...props }: React.ComponentProps<typeof UILabel>) {
  return <UILabel className={cn("text-xs", className)} {...props} />;
}

const actorTypes = ["member", "agent", "squad"] as const;
const executorKinds = [
  "fixed_actor",
  "fixed_role",
  "fallback_role",
  "capability_match",
  "manual",
] as const;

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

// The node owner is either a workflow role (resolved per instance) or a
// pinned actor. Pinning writes a fixed_actor executor strategy, which
// activation turns into the node's "owner" participant — the same record
// owner confirmations read.
function NodeOwnerEditor({
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
  const strategies = node.executor?.strategies ?? [];
  const pinned = node.owner_role
    ? undefined
    : strategies.find((strategy) => strategy.kind === "fixed_actor");
  const mode = node.owner_role ? "role" : "actor";

  const setPinnedActor = (value: string) => {
    const actor = parseActorOption(value);
    const others = strategies.filter(
      (strategy) => strategy.kind !== "fixed_actor",
    );
    if (!actor) {
      onChange({
        ...node,
        executor: others.length > 0 ? { strategies: others } : undefined,
      });
      return;
    }
    // A pinned owner still needs a landing strategy so resolution never
    // dead-ends if the actor becomes unavailable.
    const hasFallback = others.some(
      (strategy) => strategy.kind === "manual" ||
        strategy.kind === "fallback_role",
    );
    onChange({
      ...node,
      owner_role: undefined,
      executor: {
        strategies: [
          { kind: "fixed_actor", actor_type: actor.type, actor_id: actor.id },
          ...others,
          ...(hasFallback ? [] : [{ kind: "manual" }]),
        ],
      },
    });
  };

  return (
    <div className="space-y-2">
      <Label htmlFor={`node-owner-mode-${node.key}`}>
        {t(($) => $.editor.node_owner)}
      </Label>
      <select
        id={`node-owner-mode-${node.key}`}
        value={mode}
        disabled={readOnly}
        className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
        onChange={(event) => {
          if (event.target.value === "role") {
            setPinnedActor("");
            return;
          }
          onChange({ ...node, owner_role: undefined });
        }}
      >
        <option value="role">{t(($) => $.editor.node_owner_by_role)}</option>
        <option value="actor">{t(($) => $.editor.node_owner_by_actor)}</option>
      </select>
      {mode === "role" ? (
        <select
          aria-label={t(($) => $.editor.owner_role)}
          value={node.owner_role ?? ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => onChange({
            ...node,
            owner_role: event.target.value || undefined,
          })}
        >
          <option value="">—</option>
          {definition.roles.map((role) => (
            <option key={role.key} value={role.key}>{role.name}</option>
          ))}
        </select>
      ) : (
        <select
          aria-label={t(($) => $.editor.node_owner_by_actor)}
          value={pinned?.actor_type && pinned.actor_id
            ? actorOptionValue({ type: pinned.actor_type, id: pinned.actor_id })
            : ""}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => setPinnedActor(event.target.value)}
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
      <p className="text-[11px] leading-snug text-muted-foreground">
        {t(($) => $.editor.node_owner_hint)}
      </p>
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

function RoleEditor({
  roles,
  actorOptions = [],
  readOnly,
  onChange,
}: {
  roles: WorkflowRoleDefinition[];
  actorOptions?: WorkflowActorOption[];
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
          <div className="space-y-1.5">
            <Label>{t(($) => $.editor.role_default_actor)}</Label>
            <select
              aria-label={`${t(($) => $.editor.role_default_actor)} ${role.name}`}
              value={role.default_actor_type && role.default_actor_id
                ? actorOptionValue({
                    type: role.default_actor_type,
                    id: role.default_actor_id,
                  })
                : ""}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => {
                const actor = parseActorOption(event.target.value);
                const next = [...roles];
                next[index] = {
                  ...role,
                  default_actor_type: actor?.type,
                  default_actor_id: actor?.id,
                };
                onChange(next);
              }}
            >
              <option value="">{t(($) => $.editor.role_default_actor_none)}</option>
              {actorOptions
                .filter((actor) => role.allowed_actor_types.includes(actor.type))
                .map((actor) => (
                  <option
                    key={actorOptionValue(actor)}
                    value={actorOptionValue(actor)}
                  >
                    {actor.name} · {actor.type}
                  </option>
                ))}
            </select>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.editor.role_default_actor_hint)}
            </p>
          </div>
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

function AcceptanceEditor({
  definition,
  readOnly,
  onChange,
}: {
  definition: WorkflowDefinition;
  readOnly: boolean;
  onChange: (definition: WorkflowDefinition) => void;
}) {
  const { t } = useT("workflows");
  const acceptance = definition.acceptance;
  const activityNodes = definition.nodes.filter((node) => node.kind === "activity");
  const policy = acceptance.policy ?? "none";

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor="workflow-acceptance-policy">
          {t(($) => $.editor.acceptance_policy)}
        </Label>
        <select
          id="workflow-acceptance-policy"
          value={policy}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => onChange({
            ...definition,
            acceptance: event.target.value === "none"
              ? {}
              : { ...acceptance, policy: event.target.value },
          })}
        >
          <option value="none">{t(($) => $.editor.acceptance_none)}</option>
          <option value="member">{t(($) => $.editor.acceptance_member)}</option>
          <option value="node_verdict">{t(($) => $.editor.acceptance_verdict)}</option>
        </select>
      </div>
      {policy !== "none" && (
        <>
          <div className="space-y-1.5">
            <Label htmlFor="workflow-acceptance-node">
              {t(($) => $.editor.acceptance_node)}
            </Label>
            <select
              id="workflow-acceptance-node"
              value={acceptance.node_key ?? ""}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => onChange({
                ...definition,
                acceptance: { ...acceptance, node_key: event.target.value || undefined },
              })}
            >
              <option value="">—</option>
              {activityNodes.map((node) => (
                <option key={node.key} value={node.key}>{node.name}</option>
              ))}
            </select>
          </div>
          {policy === "member" && (
            <div className="space-y-1.5">
              <Label htmlFor="workflow-acceptance-role">
                {t(($) => $.editor.approver_role)}
              </Label>
              <select
                id="workflow-acceptance-role"
                value={acceptance.approver_role ?? ""}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => onChange({
                  ...definition,
                  acceptance: {
                    ...acceptance,
                    approver_role: event.target.value || undefined,
                  },
                })}
              >
                <option value="">—</option>
                {definition.roles.map((role) => (
                  <option key={role.key} value={role.key}>{role.name}</option>
                ))}
              </select>
            </div>
          )}
          <fieldset>
            <legend className="mb-1.5 text-xs font-medium">
              {t(($) => $.editor.rework_targets)}
            </legend>
            <div className="space-y-1">
              {activityNodes.map((node) => (
                <label
                  key={node.key}
                  className="flex min-h-9 items-center gap-2 rounded-md border px-2.5 text-xs"
                >
                  <input
                    type="checkbox"
                    checked={acceptance.rework_targets?.includes(node.key) ?? false}
                    disabled={readOnly || node.key === acceptance.node_key}
                    onChange={(event) => {
                      const current = acceptance.rework_targets ?? [];
                      const next = event.target.checked
                        ? [...current, node.key]
                        : current.filter((key) => key !== node.key);
                      onChange({
                        ...definition,
                        acceptance: { ...acceptance, rework_targets: next },
                      });
                    }}
                  />
                  {node.name}
                </label>
              ))}
            </div>
          </fieldset>
        </>
      )}
    </div>
  );
}

export function WorkflowDefinitionInspector({
  definition,
  actorOptions = [],
  readOnly,
  onChange,
}: {
  definition: WorkflowDefinition;
  actorOptions?: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (definition: WorkflowDefinition) => void;
}) {
  const { t } = useT("workflows");
  return (
    <div className="space-y-3">
      <InspectorSection title={t(($) => $.editor.workflow_roles)}>
        <RoleEditor
          roles={definition.roles}
          actorOptions={actorOptions}
          readOnly={readOnly}
          onChange={(roles) => onChange({ ...definition, roles })}
        />
      </InspectorSection>
      <InspectorSection title={t(($) => $.editor.workflow_acceptance)}>
        <AcceptanceEditor
          definition={definition}
          readOnly={readOnly}
          onChange={onChange}
        />
      </InspectorSection>
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
  const strategies = node.executor?.strategies ?? [];
  // Resolution strategies only apply to tasks without their own assignee,
  // so the chain stays behind an advanced toggle unless already configured.
  const [advancedOpen, setAdvancedOpen] = useState(strategies.length > 0);
  const update = (index: number, strategy: WorkflowExecutorStrategy) => {
    const next = [...strategies];
    next[index] = strategy;
    onChange({ ...node, executor: { strategies: next } });
  };
  const kindLabel = (kind: string) => {
    switch (kind) {
      case "fixed_actor":
        return t(($) => $.editor.executor_kind_fixed_actor);
      case "fixed_role":
        return t(($) => $.editor.executor_kind_fixed_role);
      case "fallback_role":
        return t(($) => $.editor.executor_kind_fallback_role);
      case "capability_match":
        return t(($) => $.editor.executor_kind_capability_match);
      case "manual":
        return t(($) => $.editor.executor_kind_manual);
      default:
        return kind;
    }
  };
  const kindHint = (kind: string) => {
    switch (kind) {
      case "fixed_actor":
        return t(($) => $.editor.executor_kind_fixed_actor_hint);
      case "fixed_role":
        return t(($) => $.editor.executor_kind_fixed_role_hint);
      case "fallback_role":
        return t(($) => $.editor.executor_kind_fallback_role_hint);
      case "capability_match":
        return t(($) => $.editor.executor_kind_capability_match_hint);
      case "manual":
        return t(($) => $.editor.executor_kind_manual_hint);
      default:
        return "";
    }
  };

  return (
    <div className="space-y-2">
      <button
        type="button"
        className="flex min-h-9 w-full items-center justify-between rounded-lg border px-3 text-xs font-medium"
        aria-expanded={advancedOpen}
        onClick={() => setAdvancedOpen((open) => !open)}
      >
        {t(($) => $.editor.executor_advanced)}
        <span aria-hidden="true" className="text-muted-foreground">
          {advancedOpen ? "−" : "+"}
        </span>
      </button>
      <p className="text-xs text-muted-foreground">
        {t(($) => $.editor.executor_advanced_hint)}
      </p>
      {advancedOpen && (
        <div className="space-y-3">
      {strategies.map((strategy, index) => (
        <div key={`${index}-${strategy.kind}`} className="space-y-3 rounded-lg border p-2.5">
          <div className="flex items-center gap-2">
            <select
              aria-label={t(($) => $.editor.executor_strategy)}
              value={strategy.kind}
              disabled={readOnly}
              className="min-h-9 min-w-0 flex-1 rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => update(index, {
                kind: event.target.value,
                // Kind-specific fields reset, but the strategy's gating
                // condition is kind-independent and must survive the switch.
                condition: strategy.condition,
              })}
            >
              {executorKinds.map((kind) => (
                <option key={kind} value={kind}>{kindLabel(kind)}</option>
              ))}
            </select>
            <RemoveButton
              label={t(($) => $.actions.remove)}
              disabled={readOnly}
              onClick={() => onChange({
                ...node,
                executor: {
                  strategies: strategies.filter((_, itemIndex) => itemIndex !== index),
                },
              })}
            />
          </div>
          {kindHint(strategy.kind) !== "" && (
            <p className="text-xs text-muted-foreground">
              {kindHint(strategy.kind)}
            </p>
          )}
          {(strategy.kind === "fixed_role" ||
            strategy.kind === "fallback_role" ||
            strategy.kind === "capability_match") && (
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.executor_role)}</Label>
              <select
                value={strategy.role ?? ""}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => update(index, {
                  ...strategy,
                  role: event.target.value || undefined,
                })}
              >
                <option value="">—</option>
                {definition.roles.map((role) => (
                  <option key={role.key} value={role.key}>{role.name}</option>
                ))}
              </select>
            </div>
          )}
          {strategy.kind === "fixed_actor" && (
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.direct_executor)}</Label>
              <select
                aria-label={t(($) => $.editor.direct_executor)}
                value={strategy.actor_type && strategy.actor_id
                  ? actorOptionValue({
                      type: strategy.actor_type,
                      id: strategy.actor_id,
                    })
                  : ""}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => {
                  const actor = parseActorOption(event.target.value);
                  update(index, {
                    kind: "fixed_actor",
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
            </div>
          )}
          {strategy.kind === "capability_match" && (
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.capability)}</Label>
              <Input
                value={strategy.capability ?? ""}
                disabled={readOnly}
                className="min-h-9 text-xs"
                onChange={(event) => update(index, {
                  ...strategy,
                  capability: event.target.value,
                })}
              />
            </div>
          )}
        </div>
      ))}
      {!readOnly && (
        <Button
          type="button"
          variant="outline"
          className="min-h-9 w-full"
          onClick={() => onChange({
            ...node,
            executor: {
              strategies: [...strategies, { kind: "manual" }],
            },
          })}
        >
          <Plus />
          {t(($) => $.editor.add_executor_strategy)}
        </Button>
      )}
        </div>
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

// Runtime decomposition at the workflow level is retired — see the comment at
// the policy select. These values are still accepted so existing definitions
// keep running; they are simply no longer offered.
function isDeprecatedIssuePolicy(policy: string | undefined): policy is string {
  return policy === "dynamic" || policy === "fixed_and_dynamic";
}

function IssueTemplateEditor({
  node,
  roles,
  actorOptions,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  roles: WorkflowRoleDefinition[];
  actorOptions: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const templates = node.issue_templates ?? [];
  const canDeclareFixed = node.issue_policy === "fixed" ||
    node.issue_policy === "fixed_and_dynamic";
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
              <Label>{t(($) => $.editor.assignee_role)}</Label>
              <select
                value={template.assignee_role ?? ""}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => update(index, {
                  ...template,
                  assignee_role: event.target.value || undefined,
                  assignee_type: undefined,
                  assignee_id: undefined,
                })}
              >
                <option value="">—</option>
                {roles.map((role) => (
                  <option key={role.key} value={role.key}>{role.name}</option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.direct_assignee)}</Label>
              <select
                aria-label={t(($) => $.editor.direct_assignee)}
                value={template.assignee_type && template.assignee_id
                  ? actorOptionValue({
                      type: template.assignee_type,
                      id: template.assignee_id,
                    })
                  : ""}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => {
                  const actor = parseActorOption(event.target.value);
                  update(index, {
                    ...template,
                    assignee_role: undefined,
                    assignee_type: actor?.type,
                    assignee_id: actor?.id,
                  });
                }}
              >
                <option value="">
                  {t(($) => $.editor.inherit_node_executor)}
                </option>
                {actorOptions.map((actor) => (
                  <option
                    key={actorOptionValue(actor)}
                    value={actorOptionValue(actor)}
                  >
                    {actor.name} · {actor.type}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label>{t(($) => $.editor.initial_status)}</Label>
              <select
                value={template.initial_status ?? "todo"}
                disabled={readOnly}
                className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                onChange={(event) => update(index, {
                  ...template,
                  initial_status: event.target.value,
                })}
              >
                {["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"].map((status) => (
                  <option key={status} value={status}>{status}</option>
                ))}
              </select>
            </div>
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

function SubmissionEditor({
  node,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const policy = node.submission_schema?.policy ?? "none";

  // Only how many results a node submits is configurable. What a node produces
  // is declared as an artifact, its conclusion is the handoff summary, and its
  // branch is a node choice — three fixed shapes, so there is no form to build.
  return (
    <div className="space-y-1.5">
      <Label>{t(($) => $.editor.submission_policy)}</Label>
      <select
        value={policy}
        disabled={readOnly}
        className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
        onChange={(event) => {
          const nextPolicy = event.target.value as
            | "none"
            | "single"
            | "per_required_task"
            | "fan_in";
          onChange({
            ...node,
            submission_schema: nextPolicy === "none"
              ? undefined
              : { policy: nextPolicy },
            completion: {
              ...(node.completion ?? {}),
              submission_required: nextPolicy === "none"
                ? false
                : (node.completion?.submission_required ?? true),
            },
          });
        }}
      >
        <option value="none">{t(($) => $.editor.submission_none)}</option>
        <option value="single">{t(($) => $.editor.submission_single)}</option>
        <option value="per_required_task">{t(($) => $.editor.submission_per_task)}</option>
        <option value="fan_in">{t(($) => $.editor.submission_fan_in)}</option>
      </select>
    </div>
  );
}

function CompletionEditor({
  node,
  roles,
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  roles: WorkflowRoleDefinition[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const completion = node.completion ?? {};
  const completionMode = workflowCompletionMode(node);
  const evaluator = node.verdict?.evaluator ?? "none";
  const confirmation = completion.confirmation ?? "none";
  const ownerRole = roles.find((role) => role.key === node.owner_role);
  // Owner confirmation needs owners that resolve to members: either a
  // member-only role or a directly pinned member owner.
  const pinsMemberOwner = !node.owner_role &&
    (node.executor?.strategies ?? []).some(
      (strategy) => strategy.kind === "fixed_actor" &&
        strategy.actor_type === "member",
    );
  const ownerCanConfirm = pinsMemberOwner || (
    ownerRole?.allowed_actor_types.length === 1 &&
    ownerRole.allowed_actor_types[0] === "member"
  );

  // The three presets cover the common Feishu-style choices; anything else
  // (manual mode, member/admin confirmations) is a custom combination
  // reachable through the advanced conditions below.
  const preset = (() => {
    if (completionMode !== "automatic") return "custom";
    if (confirmation === "none") return "automatic";
    if (confirmation === "owner_any") return "single";
    if (confirmation === "owner_all") return "multi";
    return "custom";
  })();
  const applyPreset = (
    value: "automatic" | "single" | "multi",
  ) => {
    const presetConfirmation = value === "automatic"
      ? "none"
      : value === "single"
        ? "owner_any"
        : "owner_all";
    onChange({
      ...node,
      completion: {
        ...completion,
        mode: "automatic",
        confirmation: presetConfirmation,
      },
    });
  };
  const presetOption = (
    value: "automatic" | "single" | "multi",
    label: string,
    description: string,
    disabled = false,
  ) => (
    <label
      className={`flex items-start gap-2 rounded-lg border p-2.5 text-xs ${
        preset === value ? "border-primary bg-primary/5" : ""
      } ${disabled ? "opacity-50" : ""}`}
    >
      <input
        type="radio"
        name={`completion-preset-${node.key}`}
        value={value}
        checked={preset === value}
        disabled={readOnly || disabled}
        className="mt-1"
        onChange={() => applyPreset(value)}
      />
      <span className="min-w-0">
        <span className="block text-xs font-medium">{label}</span>
        <span className="block text-[11px] leading-snug text-muted-foreground">
          {description}
        </span>
      </span>
    </label>
  );

  return (
    <div className="space-y-3">
      <fieldset className="space-y-2">
        <legend className="mb-1.5 text-xs font-medium">
          {t(($) => $.editor.completion_mode)}
        </legend>
        {presetOption(
          "automatic",
          t(($) => $.editor.completion_mode_automatic),
          t(($) => $.editor.completion_mode_automatic_help),
        )}
        {presetOption(
          "single",
          t(($) => $.editor.completion_preset_single),
          t(($) => $.editor.completion_preset_single_desc),
          !ownerCanConfirm,
        )}
        {presetOption(
          "multi",
          t(($) => $.editor.completion_preset_multi),
          t(($) => $.editor.completion_preset_multi_desc),
          !ownerCanConfirm,
        )}
        {!ownerCanConfirm && (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.editor.owner_confirmation_member_only)}
          </p>
        )}
        {preset === "custom" && (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.editor.completion_preset_custom_note)}
          </p>
        )}
      </fieldset>
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
      <div className="border-t pt-3">
        <p className="mb-3 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t(($) => $.editor.completion_conditions)}
        </p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`required-issue-outcome-${node.key}`}>
          {t(($) => $.editor.required_issue_outcome)}
        </Label>
        <select
          id={`required-issue-outcome-${node.key}`}
          value={completion.required_issue_outcome ?? "none"}
          disabled={readOnly}
          className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
          onChange={(event) => onChange({
            ...node,
            completion: {
              ...completion,
              required_issue_outcome: event.target.value as "done" | "terminal" | "none",
            },
          })}
        >
          <option value="none">{t(($) => $.editor.issue_outcome_none)}</option>
          <option value="done">{t(($) => $.editor.issue_outcome_done)}</option>
          <option value="terminal">{t(($) => $.editor.issue_outcome_terminal)}</option>
        </select>
      </div>
      <details className="rounded-lg border bg-muted/10">
        <summary className="min-h-9 cursor-pointer select-none px-3 py-2 text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {t(($) => $.editor.advanced_completion_conditions)}
        </summary>
        <div className="space-y-3 border-t p-2.5">
          <div className="space-y-1.5">
            <Label htmlFor={`completion-mode-${node.key}`}>
              {t(($) => $.editor.completion_mode_advanced)}
            </Label>
            <select
              id={`completion-mode-${node.key}`}
              value={completionMode}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => onChange({
                ...node,
                completion: {
                  ...completion,
                  mode: event.target.value as "automatic" | "manual",
                },
              })}
            >
              <option value="automatic">
                {t(($) => $.editor.completion_mode_automatic)}
              </option>
              <option value="manual">
                {t(($) => $.editor.completion_mode_manual)}
              </option>
            </select>
            <p className="text-xs text-muted-foreground">
              {completionMode === "automatic"
                ? t(($) => $.editor.completion_mode_automatic_help)
                : t(($) => $.editor.completion_mode_manual_help)}
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`verdict-evaluator-${node.key}`}>
              {t(($) => $.editor.verdict_evaluator)}
            </Label>
            <select
              id={`verdict-evaluator-${node.key}`}
              value={evaluator}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => {
                const nextEvaluator = event.target.value;
                onChange({
                  ...node,
                  verdict: nextEvaluator === "none"
                    ? undefined
                    : {
                        evaluator: nextEvaluator,
                        required_result: node.verdict?.required_result ?? "pass",
                        condition: nextEvaluator === "deterministic"
                          ? node.verdict?.condition
                          : undefined,
                      },
                  completion: {
                    ...completion,
                    verdict_required: nextEvaluator === "none"
                      ? "none"
                      : completion.verdict_required,
                  },
                });
              }}
            >
              <option value="none">{t(($) => $.editor.verdict_none)}</option>
              <option value="deterministic">{t(($) => $.editor.verdict_deterministic)}</option>
              <option value="member">{t(($) => $.editor.verdict_member)}</option>
            </select>
          </div>
          {evaluator !== "none" && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor={`required-verdict-${node.key}`}>
                  {t(($) => $.editor.required_verdict)}
                </Label>
                <select
                  id={`required-verdict-${node.key}`}
                  value={completion.verdict_required ?? "none"}
                  disabled={readOnly}
                  className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
                  onChange={(event) => onChange({
                    ...node,
                    verdict: {
                      ...node.verdict!,
                      required_result: event.target.value === "none"
                        ? undefined
                        : event.target.value,
                    },
                    completion: {
                      ...completion,
                      verdict_required: event.target.value as "none" | "pass" | "not_blocked",
                    },
                  })}
                >
                  <option value="none">{t(($) => $.editor.verdict_none)}</option>
                  <option value="pass">{t(($) => $.editor.verdict_pass)}</option>
                  <option value="not_blocked">
                    {t(($) => $.editor.verdict_not_blocked)}
                  </option>
                </select>
              </div>
              {evaluator === "deterministic" && (
                <JsonObjectEditor
                  label={t(($) => $.editor.verdict_condition)}
                  value={node.verdict?.condition}
                  readOnly={readOnly}
                  placeholder={'{"source":"node_submission","node":"review","key":"approved","op":"eq","value":true}'}
                  onChange={(condition) => onChange({
                    ...node,
                    verdict: { ...node.verdict!, condition },
                  })}
                />
              )}
            </>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`confirmation-${node.key}`}>
              {t(($) => $.editor.confirmation)}
            </Label>
            <select
              id={`confirmation-${node.key}`}
              value={confirmation}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => onChange({
                ...node,
                completion: {
                  ...completion,
                  confirmation: event.target.value as typeof confirmation,
                },
              })}
            >
              <option value="none">{t(($) => $.editor.confirmation_none)}</option>
              <option value="owner_any" disabled={!ownerCanConfirm}>
                {t(($) => $.editor.confirmation_owner_any)}
              </option>
              <option value="owner_all" disabled={!ownerCanConfirm}>
                {t(($) => $.editor.confirmation_owner_all)}
              </option>
              <option value="member_any">{t(($) => $.editor.confirmation_member_any)}</option>
              <option value="member_all">{t(($) => $.editor.confirmation_member_all)}</option>
              <option value="admin_only">{t(($) => $.editor.confirmation_admin)}</option>
            </select>
            {!ownerCanConfirm && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.editor.owner_confirmation_member_only)}
              </p>
            )}
          </div>
        </div>
      </details>
    </div>
  );
}

export function WorkflowNodeDefinitionInspector({
  node,
  definition,
  actorOptions = [],
  readOnly,
  onChange,
}: {
  node: WorkflowNodeDefinition;
  definition: WorkflowDefinition;
  actorOptions?: WorkflowActorOption[];
  readOnly: boolean;
  onChange: (node: WorkflowNodeDefinition) => void;
}) {
  const { t } = useT("workflows");
  const activity = node.kind === "activity";

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
        <>
          <div className="grid grid-cols-[minmax(0,1fr)_3rem] gap-2">
            <div className="space-y-1.5">
              <Label htmlFor="workflow-node-color">{t(($) => $.editor.node_color)}</Label>
              <Input
                id="workflow-node-color"
                value={node.color ?? ""}
                disabled={readOnly}
                placeholder="#6366f1"
                className="min-h-9 text-xs"
                onChange={(event) => onChange({
                  ...node,
                  color: event.target.value || undefined,
                })}
              />
            </div>
            <input
              type="color"
              aria-label={t(($) => $.editor.node_color)}
              value={node.color?.match(/^#[0-9a-fA-F]{6}$/) ? node.color : "#6366f1"}
              disabled={readOnly}
              className="mt-5 size-9 rounded-md border bg-background p-1"
              onChange={(event) => onChange({ ...node, color: event.target.value })}
            />
          </div>
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
          <div className="space-y-1.5">
            <Label>{t(($) => $.editor.activity_mode)}</Label>
            <select
              value={node.activity_mode ?? "work"}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => onChange({ ...node, activity_mode: event.target.value })}
            >
              <option value="work">{t(($) => $.editor.work_activity)}</option>
              <option value="acceptance">{t(($) => $.editor.acceptance_activity)}</option>
            </select>
          </div>
        </>
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
        <div>
          <h2 className="text-sm font-medium">{node.name}</h2>
          <p className="mt-1 font-mono text-xs text-muted-foreground">{node.key}</p>
        </div>
        <InspectorSection title={t(($) => $.editor.section_basic)} open>
          {basicFields}
        </InspectorSection>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div>
        <h2 className="text-sm font-medium">{node.name}</h2>
        <p className="mt-1 font-mono text-xs text-muted-foreground">{node.key}</p>
      </div>
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
            <NodeOwnerEditor
              node={node}
              definition={definition}
              actorOptions={actorOptions}
              readOnly={readOnly}
              onChange={onChange}
            />
            <fieldset>
              <legend className="mb-1.5 text-xs font-medium">
                {t(($) => $.editor.participant_roles)}
              </legend>
              <div className="space-y-1">
                {definition.roles.map((role) => (
                  <label
                    key={role.key}
                    className="flex min-h-9 items-center gap-2 rounded-md border px-2.5 text-xs"
                  >
                    <input
                      type="checkbox"
                      checked={node.participant_roles?.includes(role.key) ?? false}
                      disabled={readOnly}
                      onChange={(event) => {
                        const current = node.participant_roles ?? [];
                        onChange({
                          ...node,
                          participant_roles: event.target.checked
                            ? [...current, role.key]
                            : current.filter((key) => key !== role.key),
                        });
                      }}
                    />
                    {role.name}
                  </label>
                ))}
              </div>
            </fieldset>
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
            <Label>{t(($) => $.editor.issue_policy)}</Label>
            <select
              value={node.issue_policy ?? "none"}
              disabled={readOnly}
              className="min-h-9 w-full rounded-lg border border-input bg-background px-2.5 text-xs"
              onChange={(event) => {
                const policy = event.target.value;
                onChange({
                  ...node,
                  issue_policy: policy,
                  issue_templates: policy === "none" || policy === "dynamic"
                    ? []
                    : node.issue_templates,
                });
              }}
            >
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
          <IssueTemplateEditor
            node={node}
            roles={definition.roles}
            actorOptions={actorOptions}
            readOnly={readOnly}
            onChange={onChange}
          />
          {/* Artifacts sit under the same tab as the issues: the issue is the
              work, the artifact is what comes out of it, and both are this
              node's contract with the ones after it. */}
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
        </TabsContent>
        <TabsContent value="transition" className="space-y-3">
          <CompletionEditor
            node={node}
            roles={definition.roles}
            readOnly={readOnly}
            onChange={onChange}
          />
          <div className="space-y-3 border-t pt-3">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t(($) => $.editor.section_submission)}
            </p>
            <SubmissionEditor node={node} readOnly={readOnly} onChange={onChange} />
          </div>
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
