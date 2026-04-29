"use client";

import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { LoaderCircle, LockKeyhole } from "lucide-react";
import { toast } from "sonner";

import { AnnouncementNotifications } from "@/components/announcement-banner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { login } from "@/lib/api";
import { useRedirectIfAuthenticated } from "@/lib/use-auth-guard";
import { getDefaultRouteForRole, setStoredAuthSession } from "@/store/auth";

export default function LoginPage() {
  const navigate = useNavigate();
  const [authKey, setAuthKey] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const { isCheckingAuth } = useRedirectIfAuthenticated();

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
      });
      navigate(getDefaultRouteForRole(data.role), { replace: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : "登录失败";
      toast.error(message);
    } finally {
      setIsSubmitting(false);
    }
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
      <div className="flex w-full max-w-[440px] flex-col gap-4">
        <Card className="w-full">
          <CardHeader className="items-center gap-4 p-6 pb-2 text-center sm:p-8 sm:pb-2">
              <div className="inline-flex size-12 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-sm">
                <LockKeyhole className="size-5" />
              </div>
            <div className="flex flex-col gap-2">
              <CardTitle className="text-2xl">欢迎回来</CardTitle>
              <CardDescription className="leading-6">
                输入密钥后继续使用账号管理和图片生成功能。
              </CardDescription>
            </div>
          </CardHeader>

          <CardContent className="flex flex-col gap-6 p-6 sm:p-8">
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
              className="h-12 w-full rounded-lg"
              onClick={() => void handleLogin()}
              disabled={isSubmitting}
            >
              {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
              登录
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
