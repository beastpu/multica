"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PanelRight, PlugZap, Server } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
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

  // Connection form. Seeded from the loaded connection; the ticket field stays
  // blank (write-only) and is sent only when the admin types a new secret.
  const [swarmUrl, setSwarmUrl] = useState("");
  const [swarmUser, setSwarmUser] = useState("");
  const [ticket, setTicket] = useState("");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    setSwarmUrl(connection?.swarm_url ?? "");
    setSwarmUser(connection?.swarm_user ?? "");
    setTicket("");
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

  function connectionPayload() {
    return {
      swarm_url: swarmUrl.trim(),
      swarm_user: swarmUser.trim(),
      ticket: ticket || undefined,
    };
  }

  async function handleSave() {
    if (saving) return;
    if (!swarmUrl.trim() || !swarmUser.trim()) {
      toast.error(t(($) => $.perforce.toast_missing_fields));
      return;
    }
    if (!connection && !ticket) {
      toast.error(t(($) => $.perforce.toast_ticket_required));
      return;
    }
    setSaving(true);
    try {
      await api.savePerforceConnection(wsId, connectionPayload());
      await qc.invalidateQueries({ queryKey: perforceKeys.connection(wsId) });
      toast.success(t(($) => $.perforce.toast_saved));
      setTicket("");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.perforce.toast_save_failed));
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    if (testing) return;
    setTesting(true);
    try {
      const res = await api.testPerforceConnection(wsId, {
        swarm_url: swarmUrl.trim(),
        swarm_user: swarmUser.trim(),
        ticket: ticket || undefined,
      });
      if (res.ok) toast.success(t(($) => $.perforce.toast_test_ok));
      else toast.error(t(($) => $.perforce.toast_test_failed));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.perforce.toast_test_failed));
    } finally {
      setTesting(false);
    }
  }

  async function handleDelete() {
    if (deleting) return;
    setDeleting(true);
    try {
      await api.deletePerforceConnection(wsId);
      await qc.invalidateQueries({ queryKey: perforceKeys.connection(wsId) });
      toast.success(t(($) => $.perforce.toast_disconnected));
      setDeleteOpen(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.perforce.toast_disconnect_failed));
    } finally {
      setDeleting(false);
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
                <code className="rounded bg-muted px-1 py-0.5 text-[10px]">MULTICA_PERFORCE_SECRET_KEY</code>.
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
                  disabled={!configured}
                />
                <Field
                  id="perforce-swarm-user"
                  label={t(($) => $.perforce.field_swarm_user)}
                  placeholder="svc-multica"
                  value={swarmUser}
                  onChange={setSwarmUser}
                  disabled={!configured}
                />
                <Field
                  id="perforce-ticket"
                  label={t(($) => $.perforce.field_ticket)}
                  placeholder={
                    connection?.has_credential
                      ? t(($) => $.perforce.field_ticket_placeholder_stored)
                      : t(($) => $.perforce.field_ticket_placeholder_new)
                  }
                  value={ticket}
                  onChange={setTicket}
                  disabled={!configured}
                  type="password"
                />
                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Button size="sm" onClick={handleSave} disabled={saving || !configured}>
                    {saving ? t(($) => $.perforce.saving) : t(($) => $.perforce.save)}
                  </Button>
                  <Button variant="outline" size="sm" onClick={handleTest} disabled={testing || !configured}>
                    <PlugZap className="h-3 w-3" />
                    {testing ? t(($) => $.perforce.testing) : t(($) => $.perforce.test)}
                  </Button>
                  {connection && (
                    <Button
                      variant="outline"
                      size="sm"
                      className="ml-auto"
                      onClick={() => setDeleteOpen(true)}
                    >
                      {t(($) => $.perforce.disconnect)}
                    </Button>
                  )}
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

      <AlertDialog
        open={deleteOpen}
        onOpenChange={(v) => {
          if (!v && !deleting) setDeleteOpen(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.perforce.disconnect_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.perforce.disconnect_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t(($) => $.perforce.disconnect_confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} disabled={deleting}>
              {deleting ? t(($) => $.perforce.disconnecting) : t(($) => $.perforce.disconnect_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function Field({
  id,
  label,
  placeholder,
  value,
  onChange,
  disabled,
  type,
}: {
  id: string;
  label: string;
  placeholder?: string;
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  type?: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Input
        id={id}
        type={type}
        placeholder={placeholder}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
