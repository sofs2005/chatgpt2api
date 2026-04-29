"use client";

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertCircle, LoaderCircle } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { login } from "@/lib/api";
import { getDefaultRouteForRole, setStoredAuthSession } from "@/store/auth";

function fragmentParams() {
  const hash = typeof window === "undefined" ? "" : window.location.hash.replace(/^#/, "");
  return new URLSearchParams(hash);
}

function searchParams() {
  const search = typeof window === "undefined" ? "" : window.location.search.replace(/^\?/, "");
  return new URLSearchParams(search);
}

function sanitizeRedirectPath(path: string | null | undefined) {
  if (!path || !path.startsWith("/") || path.startsWith("//") || path.includes("://") || path.includes("\n") || path.includes("\r")) {
    return "";
  }
  return path;
}

export default function LinuxDoCallbackPage() {
  const navigate = useNavigate();
  const [errorMessage, setErrorMessage] = useState("");

  useEffect(() => {
    let active = true;
    const finishLogin = async () => {
      const params = fragmentParams();
      const query = searchParams();
      if (!params.toString() && (query.get("code") || query.get("state"))) {
        if (active) {
          setErrorMessage("Linuxdo OAuth 回调地址配置错误：请把 Linuxdo Connect 应用后台的回调地址设置为后端 /auth/linuxdo/oauth/callback，而不是前端 /auth/linuxdo/callback。");
        }
        return;
      }

      const error = params.get("error");
      if (error) {
        const message = params.get("error_description") || params.get("error_message") || error;
        if (active) {
          setErrorMessage(message);
        }
        return;
      }

      const key = params.get("key") || "";
      if (!key) {
        if (active) {
          setErrorMessage("Linuxdo 登录回调缺少本地会话密钥");
        }
        return;
      }

      try {
        const data = await login(key);
        await setStoredAuthSession({
          key,
          role: data.role,
          subjectId: data.subject_id,
          name: data.name,
          provider: data.provider,
        });
        toast.success("登录成功");
        const redirect = sanitizeRedirectPath(params.get("redirect")) || getDefaultRouteForRole(data.role);
        navigate(redirect, { replace: true });
      } catch (error) {
        if (active) {
          setErrorMessage(error instanceof Error ? error.message : "Linuxdo 登录失败");
        }
      }
    };
    void finishLogin();
    return () => {
      active = false;
    };
  }, [navigate]);

  return (
    <div className="grid min-h-[calc(100vh-1rem)] w-full place-items-center px-4 py-6">
      <Card className="w-full max-w-md rounded-[24px]">
        <CardContent className="flex flex-col items-center gap-5 p-8 text-center">
          {errorMessage ? (
            <>
              <div className="flex size-12 items-center justify-center rounded-[16px] bg-rose-50 text-rose-600">
                <AlertCircle className="size-5" />
              </div>
              <div className="space-y-2">
                <h1 className="text-xl font-semibold">Linuxdo 登录失败</h1>
                <p className="break-words text-sm leading-6 text-stone-500">{errorMessage}</p>
              </div>
              <Button className="h-10 rounded-xl px-5" onClick={() => navigate("/login", { replace: true })}>
                返回登录
              </Button>
            </>
          ) : (
            <>
              <LoaderCircle className="size-6 animate-spin text-stone-400" />
              <div className="space-y-2">
                <h1 className="text-xl font-semibold">正在完成 Linuxdo 登录</h1>
                <p className="text-sm text-stone-500">请稍候。</p>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
