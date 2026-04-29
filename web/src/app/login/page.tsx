"use client";

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { LoaderCircle, LockKeyhole, LogIn } from "lucide-react";
import { toast } from "sonner";

import { AnnouncementNotifications } from "@/components/announcement-banner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import webConfig from "@/constants/common-env";
import { fetchAuthProviders, login } from "@/lib/api";
import { useRedirectIfAuthenticated } from "@/lib/use-auth-guard";
import { getDefaultRouteForRole, setStoredAuthSession } from "@/store/auth";

export default function LoginPage() {
  const navigate = useNavigate();
  const [authKey, setAuthKey] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [linuxDoEnabled, setLinuxDoEnabled] = useState(false);
  const { isCheckingAuth } = useRedirectIfAuthenticated();

  useEffect(() => {
    let active = true;
    const loadProviders = async () => {
      try {
        const providers = await fetchAuthProviders();
        if (active) {
          setLinuxDoEnabled(Boolean(providers.linuxdo?.enabled));
        }
      } catch {
        if (active) {
          setLinuxDoEnabled(false);
        }
      }
    };
    void loadProviders();
    return () => {
      active = false;
    };
  }, []);

  const handleLogin = async () => {
    const normalizedAuthKey = authKey.trim();
    if (!normalizedAuthKey) {
      toast.error("请输入 密钥");
      return;
    }

    setIsSubmitting(true);
    try {
      const data = await login(normalizedAuthKey);
      await setStoredAuthSession({
        key: normalizedAuthKey,
        role: data.role,
        subjectId: data.subject_id,
        name: data.name,
        provider: data.provider,
      });
      navigate(getDefaultRouteForRole(data.role), { replace: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : "登录失败";
      toast.error(message);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleLinuxDoLogin = () => {
    const params = new URLSearchParams(typeof window !== "undefined" ? window.location.search : "");
    const redirectTo = params.get("redirect") || "/image";
    const base = webConfig.apiUrl.replace(/\/$/, "");
    window.location.href = `${base}/auth/linuxdo/start?redirect=${encodeURIComponent(redirectTo)}`;
  };

  if (isCheckingAuth) {
    return (
      <div className="grid min-h-[calc(100vh-1rem)] w-full place-items-center px-4 py-6">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return (
    <div className="relative grid min-h-[calc(100vh-1rem)] w-full place-items-center px-4 py-6">
      <AnnouncementNotifications target="login" className="fixed right-4 top-4 z-40 sm:right-6 sm:top-6" />
      <div className="grid w-full max-w-[920px] gap-5 lg:grid-cols-[minmax(0,1fr)_420px] lg:items-stretch">
        <section className="hidden min-h-[500px] overflow-hidden rounded-[24px] bg-[linear-gradient(135deg,#1456f0_0%,#3daeff_42%,#ea5ec1_100%)] p-7 text-white shadow-[6.5px_2px_17.5px_rgba(44,30,116,0.11)] lg:flex lg:flex-col lg:justify-between">
          <div className="flex items-center gap-2">
            <span className="size-3 rounded-full bg-white/95" />
            <span className="font-display text-lg font-semibold">chatgpt2api</span>
          </div>
          <div className="flex flex-col gap-5">
            <h1 className="font-display max-w-[420px] text-[3.5rem] leading-[1.08] font-medium">
              AI account and image workspace
            </h1>
            <p className="max-w-[360px] text-base leading-7 text-white/85">
              统一管理号池、注册流程、图片任务和运行日志。
            </p>
          </div>
          <div className="grid grid-cols-3 gap-3 text-xs font-medium text-white/88">
            <div className="rounded-[20px] bg-white/16 p-4 backdrop-blur">Account Pool</div>
            <div className="rounded-[20px] bg-white/16 p-4 backdrop-blur">Image Tasks</div>
            <div className="rounded-[20px] bg-white/16 p-4 backdrop-blur">Logs</div>
          </div>
        </section>

        <Card className="w-full justify-center rounded-[24px] shadow-[0_0_15px_rgba(44,30,116,0.16)]">
          <CardHeader className="items-center gap-5 p-7 pb-3 text-center sm:p-8 sm:pb-3">
            <div className="inline-flex size-14 items-center justify-center rounded-[16px] bg-[#181e25] text-white shadow-[0_12px_16px_-4px_rgba(36,36,36,0.08)]">
              <LockKeyhole className="size-5" />
            </div>
            <div className="flex flex-col gap-2">
              <CardTitle className="text-[2rem] leading-tight font-semibold">欢迎回来</CardTitle>
              <CardDescription className="leading-6">
                输入密钥后继续使用账号管理和图片生成功能。
              </CardDescription>
            </div>
          </CardHeader>

          <CardContent className="flex flex-col gap-6 p-7 sm:p-8">
            <div className="flex flex-col gap-2">
              <label htmlFor="auth-key" className="block text-sm font-medium text-foreground">
                密钥
              </label>
              <Input
                id="auth-key"
                type="password"
                value={authKey}
                onChange={(event) => setAuthKey(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    void handleLogin();
                  }
                }}
                placeholder="请输入密钥"
                className="h-12"
              />
            </div>

            <Button
              className="h-12 w-full"
              onClick={() => void handleLogin()}
              disabled={isSubmitting}
            >
              {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
              登录
            </Button>
            {linuxDoEnabled ? (
              <Button
                type="button"
                variant="outline"
                className="h-12 w-full"
                onClick={handleLinuxDoLogin}
                disabled={isSubmitting}
              >
                <LogIn className="size-4" />
                使用 Linuxdo 登录
              </Button>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
