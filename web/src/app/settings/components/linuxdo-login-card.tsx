"use client";

import {
  CircleHelp,
  Copy,
  LoaderCircle,
  LogIn,
  Save,
} from "lucide-react";
import { useMemo } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import webConfig from "@/constants/common-env";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  settingsDialogInputClassName,
  settingsInlineCodeClassName,
} from "./settings-ui";

const linuxDoSectionClassName = "flex flex-col gap-3";
const linuxDoFieldClassName = "gap-1.5";

function LinuxDoTip({ content }: { content: string }) {
  return (
    <span
      aria-label={content}
      title={content}
      className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
    >
      <CircleHelp className="size-4" />
    </span>
  );
}

function LinuxDoSectionHeading({
  tip,
  title,
}: {
  tip: string;
  title: string;
}) {
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <h3 className="truncate text-sm leading-6 font-semibold text-foreground">
        {title}
      </h3>
      <LinuxDoTip content={tip} />
    </div>
  );
}

function trimTrailingSlash(value: string) {
  return value.trim().replace(/\/+$/, "");
}

function buildRedirectUrlSuggestion(baseUrl: string) {
  const configuredBaseUrl = trimTrailingSlash(baseUrl);
  if (configuredBaseUrl) {
    return `${configuredBaseUrl}/auth/linuxdo/oauth/callback`;
  }

  const apiUrl = trimTrailingSlash(webConfig.apiUrl || "");
  if (apiUrl) {
    return `${apiUrl}/auth/linuxdo/oauth/callback`;
  }

  if (typeof window === "undefined") {
    return "";
  }
  return `${window.location.origin}/auth/linuxdo/oauth/callback`;
}

function buildFrontendRedirectUrlSuggestion() {
  if (typeof window === "undefined") {
    return "/auth/linuxdo/callback";
  }
  return `${window.location.origin}/auth/linuxdo/callback`;
}

export function LinuxDoLoginCard() {
  const config = useSettingsStore((state) => state.config);
  const isLoadingConfig = useSettingsStore((state) => state.isLoadingConfig);
  const isSavingConfig = useSettingsStore((state) => state.isSavingConfig);
  const setLinuxDoEnabled = useSettingsStore(
    (state) => state.setLinuxDoEnabled,
  );
  const setLinuxDoClientId = useSettingsStore(
    (state) => state.setLinuxDoClientId,
  );
  const setLinuxDoClientSecret = useSettingsStore(
    (state) => state.setLinuxDoClientSecret,
  );
  const setLinuxDoRedirectUrl = useSettingsStore(
    (state) => state.setLinuxDoRedirectUrl,
  );
  const setLinuxDoFrontendRedirectUrl = useSettingsStore(
    (state) => state.setLinuxDoFrontendRedirectUrl,
  );
  const saveConfig = useSettingsStore((state) => state.saveConfig);

  const redirectUrlSuggestion = useMemo(
    () => buildRedirectUrlSuggestion(String(config?.base_url || "")),
    [config?.base_url],
  );
  const frontendRedirectUrlSuggestion = useMemo(
    () => buildFrontendRedirectUrlSuggestion(),
    [],
  );
  const enabled = Boolean(config?.linuxdo_enabled);
  const secretConfigured = Boolean(config?.linuxdo_client_secret_configured);

  const handleUseSuggestedRedirectUrl = async () => {
    if (!redirectUrlSuggestion) {
      return;
    }
    setLinuxDoRedirectUrl(redirectUrlSuggestion);
    try {
      await navigator.clipboard.writeText(redirectUrlSuggestion);
      toast.success("回调地址已填入并复制");
    } catch {
      toast.success("回调地址已填入");
    }
  };

  const handleUseSuggestedFrontendRedirectUrl = async () => {
    if (!frontendRedirectUrlSuggestion) {
      return;
    }
    setLinuxDoFrontendRedirectUrl(frontendRedirectUrlSuggestion);
    try {
      await navigator.clipboard.writeText(frontendRedirectUrlSuggestion);
      toast.success("前端跳转地址已填入并复制");
    } catch {
      toast.success("前端跳转地址已填入");
    }
  };

  if (isLoadingConfig) {
    return (
      <SettingsCard
        icon={LogIn}
        title="Linuxdo 登录"
        description="配置 Linuxdo Connect OAuth 后，登录页会显示第三方登录入口。"
        tone="violet"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard
      icon={LogIn}
      title="Linuxdo 登录"
      description="配置 Linuxdo Connect OAuth 后，登录页会显示第三方登录入口。"
      tone="violet"
      action={
        <>
          <Badge variant={enabled ? "success" : "secondary"}>
            {enabled ? "登录入口已开启" : "登录入口已关闭"}
          </Badge>
          <Button
            type="button"
            variant={enabled ? "outline" : "default"}
            onClick={() => setLinuxDoEnabled(!enabled)}
          >
            {enabled ? "关闭登录入口" : "启用登录入口"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <section className={linuxDoSectionClassName}>
          <LinuxDoSectionHeading
            title="应用凭据"
            tip={
              secretConfigured
                ? "Client ID 来自 Linuxdo Connect 应用后台；Client Secret 已配置，留空会保留当前密钥，仅在需要更换时填写。"
                : "Client ID 和 Client Secret 来自 Linuxdo Connect 应用后台；启用 Linuxdo 登录时必须填写 Client Secret。"
            }
          />
          <div className="grid gap-3 md:grid-cols-2">
            <Field className={linuxDoFieldClassName}>
              <FieldLabel htmlFor="linuxdo-client-id">Client ID</FieldLabel>
              <Input
                id="linuxdo-client-id"
                value={String(config?.linuxdo_client_id || "")}
                onChange={(event) => setLinuxDoClientId(event.target.value)}
                placeholder="Linuxdo Connect Client ID"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
            </Field>

            <Field className={linuxDoFieldClassName}>
              <FieldLabel htmlFor="linuxdo-client-secret">
                Client Secret
              </FieldLabel>
              <Input
                id="linuxdo-client-secret"
                type="password"
                value={String(config?.linuxdo_client_secret || "")}
                onChange={(event) => setLinuxDoClientSecret(event.target.value)}
                placeholder={
                  secretConfigured
                    ? "已配置，留空则保留当前密钥"
                    : "Linuxdo Connect Client Secret"
                }
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
            </Field>
          </div>
        </section>

        <section className={linuxDoSectionClassName}>
          <LinuxDoSectionHeading
            title="回调地址"
            tip="后端 OAuth 回调地址需要填写到 Linuxdo Connect 应用后台，不要填写前端 /auth/linuxdo/callback 页面地址；前端登录完成页在同源部署时可保持 /auth/linuxdo/callback，本地 Vite 或前后端分离部署时填完整前端地址。"
          />
          <div className="grid gap-3">
            <Field className={linuxDoFieldClassName}>
              <FieldLabel htmlFor="linuxdo-backend-redirect-url">
                后端 OAuth 回调地址
              </FieldLabel>
              <Input
                id="linuxdo-backend-redirect-url"
                value={String(config?.linuxdo_redirect_url || "")}
                onChange={(event) => setLinuxDoRedirectUrl(event.target.value)}
                placeholder="https://example.com/auth/linuxdo/oauth/callback"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => void handleUseSuggestedRedirectUrl()}
                  disabled={!redirectUrlSuggestion}
                >
                  <Copy data-icon="inline-start" />
                  填入并复制建议地址
                </Button>
                {redirectUrlSuggestion ? (
                  <code className={settingsInlineCodeClassName}>
                    {redirectUrlSuggestion}
                  </code>
                ) : null}
              </div>
            </Field>

            <Field className={linuxDoFieldClassName}>
              <FieldLabel htmlFor="linuxdo-frontend-redirect-url">
                前端登录完成页
              </FieldLabel>
              <Input
                id="linuxdo-frontend-redirect-url"
                value={String(config?.linuxdo_frontend_redirect_url || "")}
                onChange={(event) =>
                  setLinuxDoFrontendRedirectUrl(event.target.value)
                }
                placeholder="/auth/linuxdo/callback"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => void handleUseSuggestedFrontendRedirectUrl()}
                  disabled={!frontendRedirectUrlSuggestion}
                >
                  <Copy data-icon="inline-start" />
                  填入并复制当前前端地址
                </Button>
                {frontendRedirectUrlSuggestion ? (
                  <code className={settingsInlineCodeClassName}>
                    {frontendRedirectUrlSuggestion}
                  </code>
                ) : null}
              </div>
            </Field>
          </div>
        </section>

        <div className="flex justify-end">
          <Button
            size="lg"
            onClick={() => void saveConfig()}
            disabled={isSavingConfig}
          >
            {isSavingConfig ? (
              <LoaderCircle data-icon="inline-start" className="animate-spin" />
            ) : (
              <Save data-icon="inline-start" />
            )}
            保存
          </Button>
        </div>
      </div>
    </SettingsCard>
  );
}
