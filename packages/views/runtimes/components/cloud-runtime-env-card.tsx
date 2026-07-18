"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, KeyRound, Loader2, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  cloudRuntimeEnvOptions,
  type CloudRuntimeEnvUpdate,
  useDeleteCloudRuntimeEnv,
  useSaveCloudRuntimeEnv,
} from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const BASE_URL_ENV = "CODEX_BASE_URL";
const MODEL_ENV = "CODEX_MODEL";
const API_KEY_ENV = "OPENAI_API_KEY";

/**
 * Admin-only card for the per-workspace cloud runtime AI connection.
 * OpenAI-compatible runtimes use non-sensitive CODEX_* values plus a
 * write-only API key delivered as Kubernetes env through the node secret.
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
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [editingApiKey, setEditingApiKey] = useState(false);

  const configuredVars = useMemo(() => envQuery.data?.env ?? [], [envQuery.data]);
  const baseUrlVar = configuredVars.find((v) => v.name === BASE_URL_ENV);
  const modelVar = configuredVars.find((v) => v.name === MODEL_ENV);
  const apiKeyVar = configuredVars.find((v) => v.name === API_KEY_ENV);
  const hasApiKey = Boolean(apiKeyVar);
  const configured = Boolean(baseUrlVar?.value || modelVar?.value || hasApiKey);

  useEffect(() => {
    if (!envQuery.data) return;
    setBaseUrl(baseUrlVar?.value ?? "");
    setModel(modelVar?.value ?? "");
    setApiKey("");
    setEditingApiKey(!apiKeyVar);
  }, [envQuery.data, baseUrlVar?.value, modelVar?.value, apiKeyVar]);

  const handleSave = async () => {
    const env: Record<string, string> = {};
    const removeEnv: string[] = [];
    const nextBaseUrl = baseUrl.trim();
    const nextModel = model.trim();
    const nextApiKey = apiKey.trim();

    if (nextBaseUrl) {
      env[BASE_URL_ENV] = nextBaseUrl;
    } else if (baseUrlVar) {
      removeEnv.push(BASE_URL_ENV);
    }
    if (nextModel) {
      env[MODEL_ENV] = nextModel;
    } else if (modelVar) {
      removeEnv.push(MODEL_ENV);
    }
    if (nextApiKey) {
      env[API_KEY_ENV] = nextApiKey;
    }

    const update: CloudRuntimeEnvUpdate = {};
    if (Object.keys(env).length > 0) update.env = env;
    if (removeEnv.length > 0) update.remove_env = removeEnv;

    if (!update.env && !update.remove_env) {
      toast.error(t(($) => $.cloud_runtime.env.empty_connection));
      return;
    }

    try {
      await saveEnv.mutateAsync(update);
      toast.success(t(($) => $.cloud_runtime.env.toast_saved));
      setApiKey("");
      setEditingApiKey(false);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.cloud_runtime.env.toast_save_failed),
      );
    }
  };

  const handleRemoveApiKey = async () => {
    try {
      await saveEnv.mutateAsync({ remove_env: [API_KEY_ENV] });
      toast.success(t(($) => $.cloud_runtime.env.toast_saved));
      setApiKey("");
      setEditingApiKey(true);
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
      setBaseUrl("");
      setModel("");
      setApiKey("");
      setEditingApiKey(true);
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
        {configured && !readOnly && (
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
        ) : (
          <div className="rounded-md border bg-background px-3 py-2">
            <div className="text-xs text-muted-foreground">
              {t(($) => $.cloud_runtime.env.provider)}
            </div>
            <div className="mt-1 text-sm font-medium">
              {t(($) => $.cloud_runtime.env.openai_compatible)}
            </div>
          </div>
        )}

        {readOnly ? (
          <p className="rounded-md border bg-background px-3 py-2 text-xs text-muted-foreground">
            {t(($) => $.cloud_runtime.env.read_only)}
          </p>
        ) : (
          <>
            <div className="grid gap-3 md:grid-cols-2">
              <TextField
                id="cloud-runtime-ai-base-url"
                label={t(($) => $.cloud_runtime.env.base_url)}
                value={baseUrl}
                onChange={setBaseUrl}
                placeholder="https://api.example.com/v1"
              />
              <TextField
                id="cloud-runtime-ai-model"
                label={t(($) => $.cloud_runtime.env.default_model)}
                value={model}
                onChange={setModel}
                placeholder="gpt-5-codex"
              />
            </div>

            <div className="space-y-1.5">
              <Label
                htmlFor="cloud-runtime-ai-api-key"
                className="text-xs text-muted-foreground"
              >
                {t(($) => $.cloud_runtime.env.api_key_optional)}
              </Label>
              {hasApiKey && !editingApiKey ? (
                <div className="flex items-center justify-between gap-3 rounded-md border bg-background px-3 py-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <CheckCircle2 className="h-4 w-4 shrink-0 text-success" />
                    <span className="text-sm font-medium">
                      {t(($) => $.cloud_runtime.env.api_key_configured)}
                    </span>
                    {apiKeyVar?.last4 && (
                      <span className="truncate text-xs text-muted-foreground">
                        {t(($) => $.cloud_runtime.env.masked_hint, {
                          last4: apiKeyVar.last4,
                        })}
                      </span>
                    )}
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2"
                      onClick={() => setEditingApiKey(true)}
                    >
                      {t(($) => $.cloud_runtime.env.replace)}
                    </Button>
                    <span className="h-4 w-px bg-border" />
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-destructive hover:text-destructive"
                      disabled={saveEnv.isPending}
                      onClick={() => void handleRemoveApiKey()}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      {t(($) => $.cloud_runtime.env.remove)}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="flex items-center gap-2">
                  <Input
                    id="cloud-runtime-ai-api-key"
                    value={apiKey}
                    onChange={(event) => setApiKey(event.target.value)}
                    placeholder={t(($) => $.cloud_runtime.env.value_placeholder)}
                    type="password"
                    className="h-9 text-sm"
                  />
                  {hasApiKey && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-9 px-2 text-xs text-muted-foreground"
                      onClick={() => {
                        setApiKey("");
                        setEditingApiKey(false);
                      }}
                    >
                      {t(($) => $.cloud_runtime.env.cancel_replace)}
                    </Button>
                  )}
                </div>
              )}
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
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
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
        className="h-9 text-sm"
      />
    </div>
  );
}
