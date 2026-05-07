"use client";

import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowRight,
  Github,
  Send,
  KeyRound,
  LoaderCircle,
  LogIn,
  MoonStar,
  ShieldCheck,
  Sun,
  UserPlus,
  UserRound,
} from "lucide-react";
import { toast } from "sonner";

import { AnnouncementNotifications } from "@/components/announcement-banner";
import { LoginPageImageStage } from "@/components/login-page-image-stage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import webConfig from "@/constants/common-env";
import { fetchAuthProviders, login, registerAccount } from "@/lib/api";
import { authSessionFromLoginResponse, setVerifiedAuthSession } from "@/lib/session";
import {
  applyColorTheme,
  getPreferredColorTheme,
  saveColorTheme,
  type ColorTheme,
} from "@/lib/theme";
import { useAppMeta } from "@/lib/use-app-meta";
import { useRedirectIfAuthenticated } from "@/lib/use-auth-guard";
import { getDefaultRouteForSession } from "@/store/auth";

const loginBackgroundClass =
  "bg-[#fff9fb] bg-[radial-gradient(rgba(20,86,240,0.12)_1px,transparent_1px),linear-gradient(145deg,#fff8fa_0%,#ffffff_48%,#f4f8ff_100%)] [background-position:0_0,center] [background-size:12px_12px,cover] dark:bg-[#090d16] dark:bg-[radial-gradient(rgba(96,165,250,0.16)_1px,transparent_1px),linear-gradient(145deg,#080b13_0%,#101827_52%,#070b12_100%)]";
const githubUrl = "https://github.com/ZyphrZero/chatgpt2api";
const telegramUrl = "https://t.me/+YBR7t_CPOYBkYzU1";

export default function LoginPage() {
  const navigate = useNavigate();
  const appMeta = useAppMeta();
  const themeToggleRef = useRef<HTMLButtonElement | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [isRegisterMode, setIsRegisterMode] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [linuxDoEnabled, setLinuxDoEnabled] = useState(false);
  const [registrationEnabled, setRegistrationEnabled] = useState(false);
  const [theme, setTheme] = useState<ColorTheme>(() => getPreferredColorTheme());
  const { isCheckingAuth } = useRedirectIfAuthenticated();

  useEffect(() => {
    let active = true;
    const loadProviders = async () => {
      try {
        const providers = await fetchAuthProviders();
        if (active) {
          setLinuxDoEnabled(Boolean(providers.linuxdo?.enabled));
          setRegistrationEnabled(Boolean(providers.registration?.enabled));
        }
      } catch {
        if (active) {
          setLinuxDoEnabled(false);
          setRegistrationEnabled(false);
        }
      }
    };
    void loadProviders();
    return () => {
      active = false;
    };
  }, []);

  const handleSubmit = async () => {
    const normalizedUsername = username.trim();
    const normalizedName = displayName.trim();
    if (!normalizedUsername) {
      toast.error("请输入用户名");
      return;
    }
    if (!password) {
      toast.error("请输入密码");
      return;
    }

    setIsSubmitting(true);
    try {
      const data = isRegisterMode
        ? await registerAccount(normalizedUsername, password, normalizedName)
        : await login(normalizedUsername, password);
      const token = String(data.token || "").trim();
      if (!token) {
        throw new Error("登录会话签发失败");
      }
      const session = authSessionFromLoginResponse(data, token);
      await setVerifiedAuthSession(session);
      toast.success(isRegisterMode ? "注册成功" : "登录成功");
      navigate(getDefaultRouteForSession(session), { replace: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : isRegisterMode ? "注册失败" : "登录失败";
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

  const handleThemeToggle = () => {
    const nextTheme = theme === "dark" ? "light" : "dark";
    const rect = themeToggleRef.current?.getBoundingClientRect();
    applyColorTheme(nextTheme, rect ? {
      origin: {
        x: rect.left + rect.width / 2,
        y: rect.top + rect.height / 2,
      },
    } : undefined);
    saveColorTheme(nextTheme);
    setTheme(nextTheme);
  };

  if (isCheckingAuth) {
    return (
      <div
        className={`${loginBackgroundClass} fixed inset-0 z-50 grid min-h-svh w-screen place-items-center overflow-hidden px-4 py-6`}
      >
        <LoaderCircle className="size-5 animate-spin text-[#45515e] dark:text-white/60" />
      </div>
    );
  }

  return (
    <div
      className={`${loginBackgroundClass} fixed inset-0 z-50 flex min-h-svh w-screen items-center justify-center overflow-y-auto px-4 py-6 font-login [align-items:safe_center] sm:px-6 lg:px-8`}
    >
      <div className="fixed right-4 top-4 z-50 flex items-center gap-2 sm:right-6 sm:top-6">
        <Button
          asChild
          type="button"
          variant="outline"
          className="h-9 rounded-full border-border/60 bg-background/80 px-3 text-muted-foreground shadow-sm backdrop-blur hover:text-foreground"
        >
          <a href={telegramUrl} target="_blank" rel="noreferrer" aria-label="加入 Telegram 群组">
            <Send data-icon="inline-start" />
            <span className="hidden sm:inline">Telegram</span>
          </a>
        </Button>
        <Button
          asChild
          type="button"
          variant="outline"
          className="h-9 rounded-full border-border/60 bg-background/80 px-3 text-muted-foreground shadow-sm backdrop-blur hover:text-foreground"
        >
          <a href={githubUrl} target="_blank" rel="noreferrer" aria-label="打开 GitHub 仓库">
            <Github data-icon="inline-start" />
            <span className="hidden sm:inline">GitHub</span>
          </a>
        </Button>
        <AnnouncementNotifications target="login" className="size-9" />
        <Button
          ref={themeToggleRef}
          type="button"
          variant="outline"
          size="icon"
          className="relative rounded-full border-border/60 bg-background/80 shadow-sm backdrop-blur"
          onClick={handleThemeToggle}
          aria-label={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"}
          title={theme === "dark" ? "浅色模式" : "深色模式"}
        >
          <Sun className="scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
          <MoonStar className="absolute scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
          <span className="sr-only">切换界面主题</span>
        </Button>
      </div>

      <div className="relative z-10 grid w-full max-w-[58rem] overflow-hidden rounded-[32px] border border-white/80 bg-white/95 shadow-[0_28px_80px_rgba(15,23,42,0.12),0_10px_28px_rgba(44,30,116,0.08)] backdrop-blur transition-[min-height] duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none dark:border-white/10 dark:bg-[#111827]/92 dark:shadow-[0_30px_90px_rgba(2,6,23,0.58),0_12px_32px_rgba(2,6,23,0.32)] lg:min-h-[39rem] lg:grid-cols-[minmax(0,28rem)_minmax(0,1fr)]">
        <section className="flex min-h-[500px] flex-col justify-center px-6 py-8 sm:px-10 lg:px-12">
          <div className="flex flex-col gap-9 transition-[gap] duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none">
            <div className="flex items-center gap-3">
              <img
                src="/logo-mark.svg"
                alt=""
                aria-hidden="true"
                className="size-11 rounded-[16px] shadow-[0_12px_16px_-4px_rgba(36,36,36,0.12)]"
              />
              <div className="grid min-w-0 leading-none">
                <div className="truncate text-sm font-semibold tracking-[-0.02em] text-[#222222] dark:text-white">
                  {appMeta.app_title || "chatgpt2api"}
                </div>
                <div className="truncate text-[10px] font-medium tracking-[0.28em] text-[#8e8e93] uppercase dark:text-white/50">
                  {appMeta.project_name && appMeta.project_name !== appMeta.app_title ? appMeta.project_name : "Control Center"}
                </div>
              </div>
            </div>

            <div className="flex flex-col gap-4">
              <div className="inline-flex w-fit items-center gap-2 rounded-full border border-[#dfe7f1] bg-white/80 px-3 py-1 text-[11px] font-semibold tracking-[0.2em] text-[#45515e] uppercase shadow-[0_4px_12px_rgba(24,40,72,0.05)] dark:border-white/10 dark:bg-white/8 dark:text-white/70 dark:shadow-[0_10px_26px_rgba(2,6,23,0.22)]">
                <ShieldCheck className="size-3.5 text-[#1456f0] dark:text-sky-300" />
                Secure Access
              </div>
              <div className="flex flex-col gap-2 transition-all duration-200 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none">
                <h1 className="text-[2.1rem] leading-[1.12] font-semibold tracking-[-0.04em] text-[#222222] transition-opacity duration-200 dark:text-white sm:text-[2.5rem]">
                  {isRegisterMode ? "CreateAccount" : "WelcomeBack"}
                </h1>
                <p className="max-w-[340px] text-sm leading-6 text-[#45515e] transition-opacity duration-200 dark:text-white/62">
                  {isRegisterMode
                    ? `创建账号后进入 ${appMeta.app_title || "chatgpt2api"} 控制台。`
                    : `使用账号和密码进入 ${appMeta.app_title || "chatgpt2api"} 控制台。`}
                </p>
              </div>
            </div>

            <form
              className="flex flex-col gap-5"
              onSubmit={(event) => {
                event.preventDefault();
                void handleSubmit();
              }}
            >
              <div className="flex flex-col gap-2">
                <label htmlFor="login-username" className="block text-sm font-semibold text-[#222222] dark:text-white/88">
                  用户名
                </label>
                <div className="relative">
                  <UserRound className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-[#8e8e93] dark:text-white/42" />
                  <Input
                    id="login-username"
                    type="text"
                    autoComplete="username"
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    placeholder="admin"
                    className="h-12 rounded-[16px] bg-white/90 pl-10 shadow-[0_6px_18px_rgba(24,40,72,0.05)] dark:border-white/12 dark:bg-white/8 dark:text-white dark:placeholder:text-white/38 dark:shadow-[0_12px_26px_rgba(2,6,23,0.24)]"
                  />
                </div>
              </div>
              <div
                className={`-m-4 grid overflow-hidden px-4 py-4 transition-[grid-template-rows,opacity,transform] duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:translate-y-0 motion-reduce:transition-none ${
                  isRegisterMode ? "grid-rows-[1fr] opacity-100 translate-y-0" : "pointer-events-none grid-rows-[0fr] opacity-0 -translate-y-1.5"
                }`}
                aria-hidden={!isRegisterMode}
              >
                <div className="min-h-0 overflow-visible">
                  <div className="flex flex-col gap-2">
                    <label htmlFor="login-display-name" className="block text-sm font-semibold text-[#222222] dark:text-white/88">
                      昵称
                    </label>
                    <div className="relative">
                      <UserPlus className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-[#8e8e93] dark:text-white/42" />
                      <Input
                        id="login-display-name"
                        type="text"
                        autoComplete="nickname"
                        tabIndex={isRegisterMode ? undefined : -1}
                        value={displayName}
                        onChange={(event) => setDisplayName(event.target.value)}
                        placeholder="可选"
                        className="h-12 rounded-[16px] bg-white/90 pl-10 shadow-[0_6px_18px_rgba(24,40,72,0.05)] dark:border-white/12 dark:bg-white/8 dark:text-white dark:placeholder:text-white/38 dark:shadow-[0_12px_26px_rgba(2,6,23,0.24)]"
                      />
                    </div>
                  </div>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                <label htmlFor="login-password" className="block text-sm font-semibold text-[#222222] dark:text-white/88">
                  密码
                </label>
                <div className="relative">
                  <KeyRound className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-[#8e8e93] dark:text-white/42" />
                  <Input
                    id="login-password"
                    type="password"
                    autoComplete={isRegisterMode ? "new-password" : "current-password"}
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder={isRegisterMode ? "至少 8 位" : "请输入密码"}
                    className="h-12 rounded-[16px] bg-white/90 pl-10 shadow-[0_6px_18px_rgba(24,40,72,0.05)] dark:border-white/12 dark:bg-white/8 dark:text-white dark:placeholder:text-white/38 dark:shadow-[0_12px_26px_rgba(2,6,23,0.24)]"
                  />
                </div>
              </div>

              <div className="flex flex-col gap-3 pt-1">
                <Button
                  type="submit"
                  variant="outline"
                  className="relative mx-auto h-12 w-[88%] overflow-hidden rounded-[1.45rem] border-slate-300/85 bg-white/72 text-[#18181b] shadow-[0_12px_28px_rgba(148,163,184,0.18)] backdrop-blur-md transition-all duration-200 hover:-translate-y-0.5 hover:border-slate-300 hover:bg-white/90 hover:text-[#18181b] hover:shadow-[0_16px_34px_rgba(148,163,184,0.22)] focus-visible:ring-slate-300/55 disabled:border-slate-200/80 disabled:bg-white/58 disabled:text-slate-500 disabled:opacity-100 disabled:shadow-none disabled:hover:translate-y-0 dark:border-white/15 dark:bg-white/12 dark:text-white dark:shadow-[0_14px_30px_rgba(2,6,23,0.32)] dark:hover:border-white/22 dark:hover:bg-white/16 dark:hover:text-white dark:hover:shadow-[0_18px_36px_rgba(2,6,23,0.38)] dark:disabled:border-white/10 dark:disabled:bg-white/8 dark:disabled:text-white/45"
                  disabled={isSubmitting}
                >
                  <span className="pointer-events-none absolute inset-x-4 top-1 h-3 rounded-full bg-white/75 blur-sm dark:bg-white/14" />
                  <span className="pointer-events-none absolute inset-[1px] rounded-[1.35rem] border border-white/55 dark:border-white/10" />
                  <span className="relative z-10 flex items-center gap-2 font-semibold tracking-[-0.01em] transition-opacity duration-150">
                    {isSubmitting ? (
                      <LoaderCircle className="size-4 animate-spin" />
                    ) : (
                      <ArrowRight className="size-4" />
                    )}
                    {isRegisterMode ? "注册并进入" : "登录控制台"}
                  </span>
                </Button>
                {registrationEnabled ? (
                  <Button
                    type="button"
                    variant="ghost"
                    className="mx-auto h-10 w-[88%] rounded-[1.2rem] text-[#45515e] hover:bg-black/5 hover:text-[#18181b] dark:text-white/62 dark:hover:bg-white/8 dark:hover:text-white"
                    onClick={() => setIsRegisterMode((value) => !value)}
                    disabled={isSubmitting}
                  >
                    <span className="transition-opacity duration-150">
                      {isRegisterMode ? "已有账号，返回登录" : "没有账号，注册一个"}
                    </span>
                  </Button>
                ) : null}
                {linuxDoEnabled ? (
                  <Button
                    type="button"
                    variant="outline"
                    className="relative mx-auto h-12 w-[88%] overflow-hidden rounded-[1.45rem] border-slate-200/95 bg-white/60 text-[#18181b] shadow-[0_10px_24px_rgba(148,163,184,0.14)] backdrop-blur-md transition-all duration-200 hover:-translate-y-0.5 hover:border-slate-300 hover:bg-white/84 hover:text-[#18181b] hover:shadow-[0_14px_30px_rgba(148,163,184,0.18)] focus-visible:ring-slate-300/55 disabled:opacity-50 disabled:hover:translate-y-0 dark:border-white/12 dark:bg-white/8 dark:text-white/88 dark:shadow-[0_12px_28px_rgba(2,6,23,0.26)] dark:hover:border-white/20 dark:hover:bg-white/13 dark:hover:text-white"
                    onClick={handleLinuxDoLogin}
                    disabled={isSubmitting}
                  >
                    <span className="pointer-events-none absolute inset-x-4 top-1 h-3 rounded-full bg-white/70 blur-sm dark:bg-white/12" />
                    <span className="pointer-events-none absolute inset-[1px] rounded-[1.35rem] border border-white/50 dark:border-white/10" />
                    <span className="relative z-10 flex items-center gap-2 font-semibold tracking-[-0.01em]">
                      <LogIn className="size-4" />
                      使用 Linuxdo 登录
                    </span>
                  </Button>
                ) : null}
              </div>
            </form>
          </div>
        </section>

        <section className="relative hidden overflow-hidden border-l border-[#e5e7eb] bg-[#f8fafc] dark:border-white/10 dark:bg-[#0c1320] lg:flex">
          <div className="pointer-events-none absolute inset-0 bg-[linear-gradient(180deg,rgba(255,255,255,0.48),transparent_38%)] dark:bg-[linear-gradient(180deg,rgba(255,255,255,0.04),transparent_38%)]" />
          <div className="relative flex flex-1 items-stretch justify-stretch">
            <LoginPageImageStage
              src={appMeta.login_page_image_url}
              mode={appMeta.login_page_image_mode}
              zoom={appMeta.login_page_image_zoom}
              positionX={appMeta.login_page_image_position_x}
              positionY={appMeta.login_page_image_position_y}
              fillParent
              frameClassName="rounded-none"
              imageClassName="rounded-none"
            />
          </div>
        </section>
      </div>
    </div>
  );
}
