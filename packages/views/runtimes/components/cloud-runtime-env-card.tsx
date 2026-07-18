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
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const ENV_NAME_RE = /^[A-Z_][A-Z0-9_]*$/;

const AI_PROVIDER_OPTIONS = [
  "multica_proxy",
  "anthropic_compatible",
  "openai_compatible",
  "custom",
] as const;

type AiProvider = (typeof AI_PROVIDER_OPTIONS)[number];

interface Row {
  name: string;
  value: string;
}

interface StructuredConnection {
  provider: AiProvider;
  baseUrl: string;
  apiKey: string;
  model: string;
}

/**
 * Admin-only card for the per-workspace cloud runtime AI connection.
 * Values are write-only: the server returns names + last-4 fingerprints, so
 * the editor starts empty and submitting merges into the encrypted env set.
 */
export function CloudRuntimeEnvCard({
  wsId,
  readOnly = false,
  className,
}: {
  wsId: string;
  readOnly?: boolean;
  className?: string;
}) {
  const { t } = useT("runtimes");
  const envQuery = useQuery(cloudRuntimeEnvOptions(wsId));
  const saveEnv = useSaveCloudRuntimeEnv(wsId);
  const deleteEnv = useDeleteCloudRuntimeEnv(wsId);
  const [connection, setConnection] = useState<StructuredConnection>({
    provider: "multica_proxy",
    baseUrl: "",
    apiKey: "",
    model: "",
  });
  const [rows, setRows] = useState<Row[]>([{ name: "", value: "" }]);

  const configuredVars = useMemo(() => envQuery.data?.env ?? [], [envQuery.data]);
  const configured = (envQuery.data?.configured === true) || configuredVars.length > 0;

  const setRow = (index: number, patch: Partial<Row>) => {
    setRows((prev) =>
      prev.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    );
  };
  const addRow = () => setRows((prev) => [...prev, { name: "", value: "" }]);
  const removeRow = (index: number) =>
    setRows((prev) =>
      prev.length === 1
        ? [{ name: "", value: "" }]
        : prev.filter((_, i) => i !== index),
    );

  const handleSave = async () => {
    const env = structuredConnectionEnv(connection);
    for (const row of rows.filter((r) => r.name.trim() || r.value.trim())) {
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

    if (Object.keys(env).length === 0) {
      toast.error(t(($) => $.cloud_runtime.env.empty_connection));
      return;
    }

    try {
      await saveEnv.mutateAsync(env);
      toast.success(t(($) => $.cloud_runtime.env.toast_saved));
      setConnection((prev) => ({ ...prev, baseUrl: "", apiKey: "", model: "" }));
      setRows([{ name: "", value: "" }]);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.cloud_runtime.env.toast_save_failed),
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
        error instanceof Error
          ? error.message
          : t(($) => $.cloud_runtime.env.toast_save_failed),
      );
    }
  };

  return (
    <section className={cn("rounded-md border bg-muted/20", className)}>
      <div className="flex items-center justify-between border-b bg-background px-3 py-2.5">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          {t(($) => $.cloud_runtime.env.title)}
        </h3>
        {configuredVars.length > 0 && !readOnly && (
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

      <div className="space-y-4 p-3">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.cloud_runtime.env.hint)}
        </p>

        {envQuery.isLoading ? (
          <div className="flex h-10 items-center justify-center">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        ) : configured ? (
          <div className="space-y-2">
            <div className="text-xs font-medium text-foreground">
              {t(($) => $.cloud_runtime.env.configured)}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {configuredVars.map((v) => (
                <Badge
                  key={v.name}
                  variant="secondary"
                  className="font-mono text-[11px]"
                >
                  {v.name}
                  {v.last4 && (
                    <span className="ml-1 text-muted-foreground">
                      {t(($) => $.cloud_runtime.env.masked_hint, {
                        last4: v.last4,
                      })}
                    </span>
                  )}
                </Badge>
              ))}
            </div>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground/70">
            {t(($) => $.cloud_runtime.env.not_configured)}
          </p>
        )}

        {readOnly ? (
          <p className="rounded-md border bg-background px-3 py-2 text-xs text-muted-foreground">
            {t(($) => $.cloud_runtime.env.read_only)}
          </p>
        ) : (
          <>
            <div className="grid gap-3 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label
                  htmlFor="cloud-runtime-ai-provider"
                  className="text-xs text-muted-foreground"
                >
                  {t(($) => $.cloud_runtime.env.provider)}
                </Label>
                <Select
                  items={AI_PROVIDER_OPTIONS.map((provider) => ({
                    value: provider,
                    label: t(($) => $.cloud_runtime.env.providers[provider]),
                  }))}
                  value={connection.provider}
                  onValueChange={(next) =>
                    setConnection((prev) => ({
                      ...prev,
                      provider: (next ?? prev.provider) as AiProvider,
                    }))
                  }
                >
                  <SelectTrigger
                    id="cloud-runtime-ai-provider"
                    className="h-9 w-full rounded-md text-sm"
                  >
                    <SelectValue>
                      {() => (
                        <span className="truncate">
                          {t(($) => $.cloud_runtime.env.providers[connection.provider])}
                        </span>
                      )}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent align="start">
                    {AI_PROVIDER_OPTIONS.map((provider) => (
                      <SelectItem key={provider} value={provider}>
                        {t(($) => $.cloud_runtime.env.providers[provider])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <TextField
                id="cloud-runtime-ai-base-url"
                label={t(($) => $.cloud_runtime.env.base_url)}
                value={connection.baseUrl}
                onChange={(baseUrl) =>
                  setConnection((prev) => ({ ...prev, baseUrl }))
                }
                placeholder="https://api.example.com/v1"
              />
              <TextField
                id="cloud-runtime-ai-api-key"
                label={t(($) => $.cloud_runtime.env.api_key)}
                value={connection.apiKey}
                onChange={(apiKey) =>
                  setConnection((prev) => ({ ...prev, apiKey }))
                }
                placeholder={t(($) => $.cloud_runtime.env.value_placeholder)}
                type="password"
              />
              <TextField
                id="cloud-runtime-ai-model"
                label={t(($) => $.cloud_runtime.env.default_model)}
                value={connection.model}
                onChange={(model) =>
                  setConnection((prev) => ({ ...prev, model }))
                }
                placeholder="claude-sonnet-4-5"
              />
            </div>

            <div className="space-y-2 rounded-md border bg-background/70 p-3">
              <div>
                <div className="text-xs font-medium text-foreground">
                  {t(($) => $.cloud_runtime.env.advanced_title)}
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {t(($) => $.cloud_runtime.env.advanced_hint)}
                </p>
              </div>

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
            </div>

            <div className="flex items-center justify-between gap-3">
              <p className="text-[11px] text-muted-foreground/70">
                {t(($) => $.cloud_runtime.env.applies_hint)}
              </p>
              <Button
                type="button"
                size="sm"
                onClick={() => void handleSave()}
                disabled={saveEnv.isPending}
                className="h-8 px-3 text-xs"
              >
                {saveEnv.isPending && (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                )}
                {saveEnv.isPending
                  ? t(($) => $.cloud_runtime.env.saving)
                  : t(($) => $.cloud_runtime.env.save)}
              </Button>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

function TextField({
  id,
  label,
  value,
  onChange,
  placeholder,
  type = "text",
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        type={type}
        className="h-9 text-sm"
      />
    </div>
  );
}

function structuredConnectionEnv(connection: StructuredConnection): Record<string, string> {
  const env: Record<string, string> = {};
  const baseUrl = connection.baseUrl.trim();
  const apiKey = connection.apiKey.trim();
  const model = connection.model.trim();
  if (baseUrl || apiKey || model) {
    env.MULTICA_CLOUD_RUNTIME_AI_PROVIDER = connection.provider;
  }

  if (baseUrl) {
    if (connection.provider === "openai_compatible") {
      env.CODEX_BASE_URL = baseUrl;
    } else {
      env.ANTHROPIC_BASE_URL = baseUrl;
    }
  }
  if (apiKey) {
    env[
      connection.provider === "openai_compatible"
        ? "OPENAI_API_KEY"
        : "ANTHROPIC_AUTH_TOKEN"
    ] = apiKey;
  }
  if (model) {
    env[
      connection.provider === "openai_compatible"
        ? "CODEX_MODEL"
        : "MULTICA_CLAUDE_MODEL"
    ] = model;
  }

  return env;
}
