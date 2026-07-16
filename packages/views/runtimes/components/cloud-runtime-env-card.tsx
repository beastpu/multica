"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  cloudRuntimeEnvOptions,
  useDeleteCloudRuntimeEnv,
  useSaveCloudRuntimeEnv,
} from "@multica/core/runtimes";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";

const ENV_NAME_RE = /^[A-Z_][A-Z0-9_]*$/;

interface Row {
  name: string;
  value: string;
}

/**
 * Admin-only card for the per-workspace cloud runtime env (LLM proxy keys).
 * Values are write-only: the server returns names + last-4 fingerprints, so
 * the editor starts empty and submitting replaces the whole set. Gated to
 * admins by the caller.
 */
export function CloudRuntimeEnvCard({ wsId }: { wsId: string }) {
  const { t } = useT("runtimes");
  const envQuery = useQuery(cloudRuntimeEnvOptions(wsId));
  const saveEnv = useSaveCloudRuntimeEnv(wsId);
  const deleteEnv = useDeleteCloudRuntimeEnv(wsId);
  const [rows, setRows] = useState<Row[]>([{ name: "", value: "" }]);

  const existing = envQuery.data;
  const configuredVars = useMemo(() => existing?.env ?? [], [existing]);

  const setRow = (index: number, patch: Partial<Row>) => {
    setRows((prev) =>
      prev.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    );
  };
  const addRow = () => setRows((prev) => [...prev, { name: "", value: "" }]);
  const removeRow = (index: number) =>
    setRows((prev) =>
      prev.length === 1 ? [{ name: "", value: "" }] : prev.filter((_, i) => i !== index),
    );

  const handleSave = async () => {
    const filled = rows.filter((r) => r.name.trim() || r.value.trim());
    if (filled.length === 0) {
      toast.error(t(($) => $.cloud_runtime.env.empty));
      return;
    }
    const env: Record<string, string> = {};
    for (const row of filled) {
      const name = row.name.trim();
      if (!ENV_NAME_RE.test(name)) {
        toast.error(t(($) => $.cloud_runtime.env.invalid_name));
        return;
      }
      if (!row.value.trim()) {
        toast.error(t(($) => $.cloud_runtime.env.empty));
        return;
      }
      env[name] = row.value;
    }
    try {
      await saveEnv.mutateAsync(env);
      toast.success(t(($) => $.cloud_runtime.env.toast_saved));
      setRows([{ name: "", value: "" }]);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.cloud_runtime.env.toast_save_failed),
      );
    }
  };

  const handleClear = async () => {
    if (!window.confirm(t(($) => $.cloud_runtime.env.clear_confirm))) return;
    try {
      await deleteEnv.mutateAsync();
      toast.success(t(($) => $.cloud_runtime.env.toast_cleared));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.cloud_runtime.env.toast_save_failed),
      );
    }
  };

  return (
    <section className="rounded-md border bg-muted/20">
      <div className="flex items-center justify-between border-b bg-background px-3 py-2.5">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          {t(($) => $.cloud_runtime.env.title)}
        </h3>
        {configuredVars.length > 0 && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => void handleClear()}
            disabled={deleteEnv.isPending}
            className="h-7 px-2 text-muted-foreground"
          >
            {t(($) => $.cloud_runtime.env.clear)}
          </Button>
        )}
      </div>

      <div className="space-y-3 p-3">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.cloud_runtime.env.hint)}
        </p>

        {envQuery.isLoading ? (
          <div className="flex h-10 items-center justify-center">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        ) : configuredVars.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {configuredVars.map((v) => (
              <Badge key={v.name} variant="secondary" className="font-mono text-[11px]">
                {v.name}
                {v.last4 && (
                  <span className="ml-1 text-muted-foreground">
                    {t(($) => $.cloud_runtime.env.masked_hint, { last4: v.last4 })}
                  </span>
                )}
              </Badge>
            ))}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground/70">
            {t(($) => $.cloud_runtime.env.not_configured)}
          </p>
        )}

        <div className="space-y-2">
          {rows.map((row, index) => (
            <div key={index} className="flex items-center gap-2">
              <Input
                value={row.name}
                onChange={(e) => setRow(index, { name: e.target.value })}
                placeholder={t(($) => $.cloud_runtime.env.name_placeholder)}
                className="h-8 flex-1 font-mono text-xs"
                aria-label={t(($) => $.cloud_runtime.env.name)}
              />
              <Input
                value={row.value}
                onChange={(e) => setRow(index, { value: e.target.value })}
                placeholder={t(($) => $.cloud_runtime.env.value_placeholder)}
                type="password"
                className="h-8 flex-1 text-xs"
                aria-label={t(($) => $.cloud_runtime.env.value)}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => removeRow(index)}
                className="h-8 w-8 shrink-0 text-muted-foreground"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
        </div>

        <div className="flex items-center justify-between">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={addRow}
            className="h-7 px-2 text-xs"
          >
            <Plus className="h-3.5 w-3.5" />
            {t(($) => $.cloud_runtime.env.add)}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={() => void handleSave()}
            disabled={saveEnv.isPending}
            className="h-7 px-3 text-xs"
          >
            {saveEnv.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {saveEnv.isPending
              ? t(($) => $.cloud_runtime.env.saving)
              : t(($) => $.cloud_runtime.env.save)}
          </Button>
        </div>

        <p className="text-[11px] text-muted-foreground/70">
          {t(($) => $.cloud_runtime.env.applies_hint)}
        </p>
      </div>
    </section>
  );
}
