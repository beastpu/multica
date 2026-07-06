"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@multica/ui/components/ui/select";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  CAPABILITY_P4_ASSESSMENT,
  agentListOptions,
  workspaceCapabilityOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { api } from "@multica/core/api";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

// P4 assessment capability role (docs/agent-fix-p4-assessment-issue-design.md,
// "触发与能力角色"): the workspace designates one agent to execute assessment
// runs. The trigger is fail-closed — no designated agent, no new runs — so
// this card is both the on switch and the off switch (Clear = DELETE).
//
// The runtime constraint (assessments need inner-network P4/Swarm access, so
// the agent must sit on a daemon-served local runtime) is enforced
// server-side on PUT; the selector pre-empts it by disabling non-local
// agents, and known server rejection reasons are mapped to readable copy.
export function AssessmentCapabilitySection() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const navigation = useNavigation();
  const queryClient = useQueryClient();

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: capability = null } = useQuery(
    workspaceCapabilityOptions(wsId, CAPABILITY_P4_ASSESSMENT),
  );

  // null = untouched, follow the server value. Avoids a seeding effect and
  // keeps the draft in sync when the query (re)loads.
  const [draftAgentId, setDraftAgentId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [clearing, setClearing] = useState(false);

  const configuredAgentId = capability?.agent_id ?? "";
  const selectedAgentId = draftAgentId ?? configuredAgentId;
  const isDirty = selectedAgentId !== "" && selectedAgentId !== configuredAgentId;

  const activeAgents = agents.filter((a) => !a.archived_at);
  const selectedAgent = activeAgents.find((a) => a.id === selectedAgentId) ?? null;
  // Configured agent may have been archived since — fall back to the name the
  // capability response carries so the trigger never shows a bare UUID.
  const selectedLabel =
    selectedAgent?.name ??
    (selectedAgentId && selectedAgentId === configuredAgentId ? capability?.agent_name : null) ??
    null;

  // Known PUT rejection reasons from
  // server/internal/handler/workspace_capability.go. Unknown reasons fall
  // through to a generic failure carrying the raw server text.
  function saveErrorCopy(message: string): string {
    switch (message) {
      case "agent not found in this workspace":
        return t(($) => $.assessment.error_agent_not_in_workspace);
      case "agent is archived":
        return t(($) => $.assessment.error_agent_archived);
      case "agent has no runtime":
        return t(($) => $.assessment.error_agent_no_runtime);
      case "agent runtime not found":
        return t(($) => $.assessment.error_agent_runtime_not_found);
      case "capability agent must run on a daemon (local) runtime":
        return t(($) => $.assessment.error_agent_runtime_not_local);
      default:
        return t(($) => $.assessment.save_failed_reason, { reason: message });
    }
  }

  async function handleSave() {
    if (!selectedAgentId) return;
    setSaving(true);
    try {
      await api.putWorkspaceCapability(wsId, CAPABILITY_P4_ASSESSMENT, {
        agent_id: selectedAgentId,
      });
      await queryClient.invalidateQueries({
        queryKey: workspaceKeys.capability(wsId, CAPABILITY_P4_ASSESSMENT),
      });
      setDraftAgentId(null);
      toast.success(t(($) => $.assessment.saved));
    } catch (e) {
      toast.error(
        e instanceof Error ? saveErrorCopy(e.message) : t(($) => $.assessment.save_failed),
      );
    } finally {
      setSaving(false);
    }
  }

  async function handleClear() {
    setClearing(true);
    try {
      await api.deleteWorkspaceCapability(wsId, CAPABILITY_P4_ASSESSMENT);
      await queryClient.invalidateQueries({
        queryKey: workspaceKeys.capability(wsId, CAPABILITY_P4_ASSESSMENT),
      });
      setDraftAgentId(null);
      toast.success(t(($) => $.assessment.cleared));
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? t(($) => $.assessment.save_failed_reason, { reason: e.message })
          : t(($) => $.assessment.clear_failed),
      );
    } finally {
      setClearing(false);
    }
  }

  return (
    <Card>
      <CardContent className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2">
              <p className="text-sm font-medium">{t(($) => $.assessment.title)}</p>
              {capability ? (
                <Badge variant="outline" className="border-transparent bg-success/10 text-success">
                  <Check />
                  {capability.agent_name}
                </Badge>
              ) : (
                <Badge variant="outline" className="text-muted-foreground">
                  {t(($) => $.assessment.not_configured)}
                </Badge>
              )}
            </div>
            <p className="text-xs text-muted-foreground">{t(($) => $.assessment.description)}</p>
            <p className="text-xs text-muted-foreground">{t(($) => $.assessment.runtime_hint)}</p>
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="shrink-0"
            onClick={() => navigation.push(wsPaths.agents())}
          >
            <Plus className="h-3.5 w-3.5" />
            {t(($) => $.assessment.create_agent)}
          </Button>
        </div>

        <div className="flex flex-wrap items-end gap-2">
          <label className="min-w-56 flex-1 space-y-1.5 text-xs font-medium">
            {t(($) => $.assessment.agent_label)}
            <Select
              value={selectedAgentId || undefined}
              onValueChange={(value) => setDraftAgentId(value ?? "")}
            >
              <SelectTrigger
                size="sm"
                className="w-full"
                aria-label={t(($) => $.assessment.agent_label)}
              >
                <span
                  className={`min-w-0 flex-1 truncate text-left ${selectedLabel ? "" : "text-muted-foreground"}`}
                >
                  {selectedLabel ?? t(($) => $.assessment.select_placeholder)}
                </span>
              </SelectTrigger>
              <SelectContent align="start">
                {activeAgents.length === 0 && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    {t(($) => $.assessment.no_agents)}
                  </div>
                )}
                {activeAgents.map((agent) => (
                  <SelectItem
                    key={agent.id}
                    value={agent.id}
                    disabled={agent.runtime_mode !== "local"}
                  >
                    <span className="min-w-0 flex-1 truncate">{agent.name}</span>
                    {agent.runtime_mode !== "local" && (
                      <span className="shrink-0 text-[10px] text-muted-foreground">
                        {t(($) => $.assessment.agent_not_local)}
                      </span>
                    )}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
          <Button size="sm" onClick={handleSave} disabled={saving || clearing || !isDirty}>
            {saving ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Save className="h-3.5 w-3.5" />
            )}
            {saving ? t(($) => $.assessment.saving) : t(($) => $.assessment.save)}
          </Button>
          {capability && (
            <Button
              size="sm"
              variant="outline"
              onClick={handleClear}
              disabled={saving || clearing}
            >
              {clearing ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
              {clearing ? t(($) => $.assessment.clearing) : t(($) => $.assessment.clear)}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
