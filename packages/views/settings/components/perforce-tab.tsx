"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PanelRight, Server } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  derivePerforceSettings,
  perforceConnectionOptions,
  perforceKeys,
} from "@multica/core/perforce";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";
import { useT } from "../../i18n";

type SettingsKey = "perforce_enabled" | "perforce_review_sidebar_enabled";

export function PerforceTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const { data } = useQuery(perforceConnectionOptions(wsId));
  const connection = data?.connection ?? null;
  const configured = data?.configured ?? false;
  const canManage = data?.can_manage === true;

  const flags = derivePerforceSettings(workspace);
  const [savingKey, setSavingKey] = useState<SettingsKey | null>(null);

  // Connection form. v1 is webhook-push only, so the only field is the Swarm
  // URL the webhook routes on — no credentials.
  const [swarmUrl, setSwarmUrl] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setSwarmUrl(connection?.swarm_url ?? "");
  }, [connection]);

  async function persistSetting(key: SettingsKey, next: boolean) {
    if (!workspace || savingKey) return;
    setSavingKey(key);
    try {
      const merged = {
        ...((workspace.settings as Record<string, unknown>) ?? {}),
        [key]: next,
      };
      const updated = await api.updateWorkspace(workspace.id, { settings: merged });
      qc.setQueryData(workspaceKeys.list(), (old: Workspace[] | undefined) =>
        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.perforce.toast_failed));
    } finally {
      setSavingKey(null);
    }
  }

  async function handleSave() {
    if (saving) return;
    if (!swarmUrl.trim()) {
      toast.error(t(($) => $.perforce.toast_missing_fields));
      return;
    }
    setSaving(true);
    try {
      await api.savePerforceConnection(wsId, { swarm_url: swarmUrl.trim() });
      await qc.invalidateQueries({ queryKey: perforceKeys.connection(wsId) });
      toast.success(t(($) => $.perforce.toast_saved));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.perforce.toast_save_failed));
    } finally {
      setSaving(false);
    }
  }

  if (!workspace) return null;

  return (
    <div className="space-y-8">
      <section className="space-y-1">
        <p className="text-sm text-muted-foreground">{t(($) => $.perforce.page_description)}</p>
      </section>

      <section className="space-y-3">
        <Card>
          <CardContent>
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">
                  <Server className="h-4 w-4" />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="perforce-master" className="text-sm font-medium">
                    {t(($) => $.perforce.section_master)}
                  </Label>
                  <p className="text-sm text-muted-foreground">
                    {flags.enabled
                      ? t(($) => $.perforce.master_description_on)
                      : t(($) => $.perforce.master_description_off)}
                  </p>
                </div>
              </div>
              <Switch
                id="perforce-master"
                checked={flags.enabled}
                onCheckedChange={(v) => persistSetting("perforce_enabled", v)}
                disabled={!canManage || savingKey === "perforce_enabled"}
              />
            </div>
          </CardContent>
        </Card>
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t(($) => $.perforce.section_connection)}</h2>
        <Card>
          <CardContent className="space-y-4">
            {!configured && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.perforce.not_configured)}{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-[10px]">
                  MULTICA_P4_SWARM_WEBHOOK_TOKEN
                </code>
                .
              </p>
            )}
            {!canManage ? (
              <p className="text-xs text-muted-foreground">{t(($) => $.perforce.read_only_hint)}</p>
            ) : (
              <>
                <Field
                  id="perforce-swarm-url"
                  label={t(($) => $.perforce.field_swarm_url)}
                  placeholder="http://igame-swarm.lilithgame.com"
                  value={swarmUrl}
                  onChange={setSwarmUrl}
                />
                <p className="text-xs text-muted-foreground">{t(($) => $.perforce.connection_hint)}</p>
                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Button size="sm" onClick={handleSave} disabled={saving}>
                    {saving ? t(($) => $.perforce.saving) : t(($) => $.perforce.save)}
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t(($) => $.perforce.section_features)}</h2>
        <Card>
          <CardContent>
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">
                  <PanelRight className="h-4 w-4" />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="perforce-review-sidebar" className="text-sm font-medium">
                    {t(($) => $.perforce.feature_review_sidebar_label)}
                  </Label>
                  <p className="text-sm text-muted-foreground">
                    {t(($) => $.perforce.feature_review_sidebar_description)}
                  </p>
                </div>
              </div>
              <Switch
                id="perforce-review-sidebar"
                checked={flags.reviewSidebar}
                disabled={!canManage || !flags.enabled || savingKey === "perforce_review_sidebar_enabled"}
                onCheckedChange={(v) => persistSetting("perforce_review_sidebar_enabled", v)}
              />
            </div>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}

function Field({
  id,
  label,
  placeholder,
  value,
  onChange,
}: {
  id: string;
  label: string;
  placeholder?: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Input
        id={id}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
