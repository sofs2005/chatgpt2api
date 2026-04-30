"use client";

import { useEffect, useState } from "react";
import { ChevronDown, LogOut, Moon, Sun, UserCircle2 } from "lucide-react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";

import { AnnouncementNotifications } from "@/components/announcement-banner";
import { ImageTaskQueue } from "@/components/image-task-queue";
import webConfig from "@/constants/common-env";
import {
  AUTH_SESSION_CHANGE_EVENT,
  clearVerifiedAuthSession,
  getCachedAuthSession,
  getVerifiedAuthSession,
} from "@/lib/session";
import {
  canAccessPath,
  getDefaultRouteForSession,
  hasAPIPermission,
  type StoredAuthSession,
} from "@/store/auth";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { fetchAccounts, type Account } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  applyColorTheme,
  getPreferredColorTheme,
  saveColorTheme,
  type ColorTheme,
} from "@/lib/theme";

const navItems = [
  { href: "/image", label: "创作台" },
  { href: "/accounts", label: "号池管理" },
  { href: "/register", label: "注册机" },
  { href: "/image-manager", label: "图片库" },
  { href: "/users", label: "用户管理" },
  { href: "/rbac", label: "角色权限" },
  { href: "/logs", label: "日志管理" },
  { href: "/settings", label: "设置" },
];
const profileNavItem = { href: "/profile", label: "个人中心" };
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";

function formatAvailableQuota(accounts: Account[]) {
  const availableAccounts = accounts.filter((account) => account.status !== "禁用");
  return String(availableAccounts.reduce((sum, account) => sum + Math.max(0, account.quota), 0));
}

function ThemeToggleButton({
  theme,
  onToggle,
  className,
}: {
  theme: ColorTheme;
  onToggle: () => void;
  className?: string;
}) {
  const dark = theme === "dark";

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={cn("size-8 rounded-full", className)}
      onClick={onToggle}
      aria-label={dark ? "切换到浅色模式" : "切换到深色模式"}
      title={dark ? "浅色模式" : "深色模式"}
    >
      {dark ? <Sun /> : <Moon />}
    </Button>
  );
}

type NavItem = {
  href: string;
  label: string;
};

function isActivePath(pathname: string, href: string) {
  return pathname === href || pathname.startsWith(`${href}/`);
}

function NavPill({ item, pathname }: { item: NavItem; pathname: string }) {
  const active = isActivePath(pathname, item.href);

  return (
    <NavLink
      to={item.href}
      className={() =>
        cn(
          "relative shrink-0 whitespace-nowrap rounded-full px-3 py-1.5 text-[13px] font-medium transition sm:text-sm",
          active
            ? "bg-black/[0.06] text-[#18181b] shadow-[inset_0_0_0_1px_rgba(20,86,240,0.08)] dark:bg-accent dark:text-accent-foreground"
            : "text-[#45515e] hover:bg-black/[0.05] hover:text-[#18181b] dark:text-muted-foreground dark:hover:bg-accent dark:hover:text-accent-foreground",
        )
      }
    >
      {item.label}
    </NavLink>
  );
}

function AccountMenu({
  session,
  roleLabel,
  availableQuota,
  pathname,
  onLogout,
}: {
  session: StoredAuthSession;
  roleLabel: string;
  availableQuota: string;
  pathname: string;
  onLogout: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const displayName = session.name || roleLabel;
  const initial = (displayName.trim() || "U").slice(0, 1).toUpperCase();
  const profileActive = isActivePath(pathname, profileNavItem.href);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className={cn(
            "h-9 rounded-full px-2.5 shadow-none",
            profileActive ? "border-[#1456f0]/30 bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300" : "",
          )}
          aria-label="账号菜单"
        >
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
            {initial}
          </span>
          <span className="hidden max-w-[120px] truncate lg:inline">{displayName}</span>
          <ChevronDown />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        sideOffset={8}
        className="w-72 border-border bg-card p-2 text-card-foreground shadow-[0_20px_60px_-30px_rgba(15,23,42,0.45)] dark:border-border dark:bg-card"
      >
        <div className="flex flex-col gap-2">
          <div className="rounded-xl bg-muted/50 p-3">
            <div className="flex min-w-0 items-center gap-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">
                {initial}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-semibold text-foreground">{displayName}</div>
                <code className="block truncate font-mono text-xs text-muted-foreground">
                  {session.subjectId || session.role}
                </code>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-2 text-xs">
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">角色</div>
              <div className="truncate font-medium text-foreground">{roleLabel}</div>
            </div>
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">额度</div>
              <div className="truncate font-medium text-foreground">{availableQuota}</div>
            </div>
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">版本</div>
              <div className="truncate font-medium text-foreground">v{webConfig.appVersion}</div>
            </div>
          </div>

          <Link
            to={profileNavItem.href}
            className={cn(
              "flex items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium transition hover:bg-accent hover:text-accent-foreground",
              profileActive ? "bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300" : "text-foreground",
            )}
            onClick={() => setOpen(false)}
          >
            <UserCircle2 className="size-4" />
            个人中心
          </Link>

          <button
            type="button"
            className="flex items-center justify-center gap-2 rounded-xl px-3 py-2 text-sm font-medium text-rose-600 transition hover:bg-rose-50 hover:text-rose-700 dark:hover:bg-rose-950/30"
            onClick={() => {
              setOpen(false);
              void onLogout();
            }}
          >
            <LogOut className="size-4" />
            退出登录
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

export function TopNav() {
  const location = useLocation();
  const navigate = useNavigate();
  const pathname = location.pathname.replace(/\/+$/, "") || "/";
  const [session, setSession] = useState<StoredAuthSession | null | undefined>(() => getCachedAuthSession());
  const [theme, setTheme] = useState<ColorTheme>(() => getPreferredColorTheme());
  const [availableQuota, setAvailableQuota] = useState("--");

  useEffect(() => {
    applyColorTheme(theme);
  }, [theme]);

  useEffect(() => {
    let active = true;

    const load = async () => {
      if (pathname === "/login") {
        if (!active) {
          return;
        }
        setSession(null);
        return;
      }

      const storedSession = await getVerifiedAuthSession();
      if (!active) {
        return;
      }
      setSession(storedSession);
    };

    void load();
    return () => {
      active = false;
    };
  }, [pathname]);

  useEffect(() => {
    const handleSessionChange = () => {
      setSession(getCachedAuthSession() ?? null);
    };
    window.addEventListener(AUTH_SESSION_CHANGE_EVENT, handleSessionChange);
    return () => {
      window.removeEventListener(AUTH_SESSION_CHANGE_EVENT, handleSessionChange);
    };
  }, []);

  useEffect(() => {
    if (!hasAPIPermission(session, "GET", "/api/accounts")) {
      setAvailableQuota("--");
      return;
    }

    let active = true;
    const loadQuota = async () => {
      try {
        const data = await fetchAccounts();
        if (active) {
          setAvailableQuota(formatAvailableQuota(data.items));
        }
      } catch {
        if (active) {
          setAvailableQuota((current) => (current === "加载中..." ? "--" : current));
        }
      }
    };
    const handleRefresh = () => {
      void loadQuota();
    };

    setAvailableQuota("加载中...");
    void loadQuota();
    window.addEventListener("focus", handleRefresh);
    window.addEventListener(QUOTA_REFRESH_EVENT, handleRefresh);
    return () => {
      active = false;
      window.removeEventListener("focus", handleRefresh);
      window.removeEventListener(QUOTA_REFRESH_EVENT, handleRefresh);
    };
  }, [session]);

  const handleLogout = async () => {
    await clearVerifiedAuthSession();
    navigate("/login", { replace: true });
  };

  const handleThemeToggle = () => {
    setTheme((currentTheme) => {
      const nextTheme = currentTheme === "dark" ? "light" : "dark";
      applyColorTheme(nextTheme);
      saveColorTheme(nextTheme);
      return nextTheme;
    });
  };

  if (pathname === "/login" || pathname === "/auth/linuxdo/callback" || session === undefined || !session) {
    return null;
  }

  const visibleNavItems = navItems.filter((item) => canAccessPath(session, item.href));
  const homePath = getDefaultRouteForSession(session);
  const roleLabel = session.role === "admin" ? "管理员" : session.roleName || (session.provider === "linuxdo" ? "Linuxdo 用户" : "普通用户");
  const canAccessImageTasks = canAccessPath(session, "/image");

  return (
    <header className="sticky top-3 z-40 rounded-[24px] border border-border bg-card/90 shadow-[0_0_22.576px_rgba(44,74,116,0.09)] backdrop-blur dark:border-border dark:bg-card/92">
      <div className="flex min-h-14 flex-col gap-2 px-3 py-2 lg:flex-row lg:items-center lg:justify-between lg:gap-4 lg:px-4">
        <div className="flex items-center justify-between gap-3 lg:justify-start">
          <Link
            to={homePath}
            className="font-display inline-flex shrink-0 items-center gap-2 py-1 text-[15px] font-semibold text-[#18181b] transition hover:text-[#1456f0] dark:text-foreground dark:hover:text-sky-300"
          >
            <img
              src="/logo-mark.svg"
              alt=""
              aria-hidden="true"
              className="size-7 rounded-[10px] shadow-[0_4px_10px_rgba(184,90,127,0.16)]"
            />
            chatgpt2api
          </Link>
          <div className="ml-auto flex shrink-0 items-center gap-1 lg:hidden">
            {canAccessImageTasks ? <ImageTaskQueue className="size-8 px-0" /> : null}
            <AnnouncementNotifications target="image" className="size-8" />
            <ThemeToggleButton theme={theme} onToggle={handleThemeToggle} />
            <AccountMenu
              session={session}
              roleLabel={roleLabel}
              availableQuota={availableQuota}
              pathname={pathname}
              onLogout={handleLogout}
            />
          </div>
        </div>
        <nav className="hide-scrollbar -mx-1 flex min-w-0 flex-1 gap-1 overflow-x-auto px-1 lg:mx-0 lg:justify-center lg:gap-1.5 lg:px-0">
          {visibleNavItems.map((item) => (
            <NavPill key={item.href} item={item} pathname={pathname} />
          ))}
        </nav>
        <div className="hidden items-center justify-end gap-1.5 lg:flex">
          {canAccessImageTasks ? <ImageTaskQueue /> : null}
          <AnnouncementNotifications target="image" className="size-8" />
          <ThemeToggleButton theme={theme} onToggle={handleThemeToggle} />
          <AccountMenu
            session={session}
            roleLabel={roleLabel}
            availableQuota={availableQuota}
            pathname={pathname}
            onLogout={handleLogout}
          />
        </div>
      </div>
    </header>
  );
}
