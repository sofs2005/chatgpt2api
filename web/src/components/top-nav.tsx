"use client";

import { useEffect, useState } from "react";
import { Github } from "lucide-react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";

import webConfig from "@/constants/common-env";
import { clearStoredAuthSession, getStoredAuthSession, type StoredAuthSession } from "@/store/auth";
import { cn } from "@/lib/utils";

const adminNavItems = [
  { href: "/image", label: "画图" },
  { href: "/accounts", label: "号池管理" },
  { href: "/register", label: "注册机" },
  { href: "/image-manager", label: "图片管理" },
  { href: "/logs", label: "日志管理" },
  { href: "/settings", label: "设置" },
];

const userNavItems = [{ href: "/image", label: "画图" }];

export function TopNav() {
  const location = useLocation();
  const navigate = useNavigate();
  const pathname = location.pathname.replace(/\/+$/, "") || "/";
  const [session, setSession] = useState<StoredAuthSession | null | undefined>(undefined);

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

      const storedSession = await getStoredAuthSession();
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

  const handleLogout = async () => {
    await clearStoredAuthSession();
    navigate("/login", { replace: true });
  };

  if (pathname === "/login" || session === undefined || !session) {
    return null;
  }

  const navItems = session.role === "admin" ? adminNavItems : userNavItems;
  const roleLabel = session.role === "admin" ? "管理员" : "普通用户";

  return (
    <header className="sticky top-3 z-40 rounded-lg border border-border bg-background/95 shadow-sm backdrop-blur">
      <div className="flex min-h-12 flex-col gap-2 px-3 py-2 sm:h-12 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-4 sm:py-0">
        <div className="flex items-center justify-between gap-3 sm:justify-start">
          <Link
            to="/image"
            className="shrink-0 py-1 text-[15px] font-semibold tracking-tight text-foreground transition hover:text-muted-foreground"
          >
            chatgpt2api
          </Link>
          <a
            href="https://github.com/basketikun/chatgpt2api"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 py-1 text-sm text-muted-foreground transition hover:text-foreground"
            aria-label="GitHub repository"
          >
            <Github className="size-4" />
            <span className="hidden md:inline">GitHub</span>
          </a>
          <button
            type="button"
            className="ml-auto shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground sm:hidden"
            onClick={() => void handleLogout()}
          >
            退出
          </button>
        </div>
        <nav className="hide-scrollbar -mx-1 flex min-w-0 flex-1 gap-1 overflow-x-auto px-1 sm:mx-0 sm:justify-center sm:gap-1.5 sm:overflow-visible sm:px-0">
          {navItems.map((item) => {
            const active = pathname === item.href;
            return (
              <NavLink
                key={item.href}
                to={item.href}
                className={() =>
                  cn(
                    "relative shrink-0 whitespace-nowrap rounded-md px-2.5 py-1.5 text-[13px] font-medium transition sm:text-sm",
                    active
                      ? "bg-primary text-primary-foreground shadow-xs"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )
                }
              >
                {item.label}
              </NavLink>
            );
          })}
        </nav>
        <div className="hidden items-center justify-end gap-2 sm:flex sm:gap-3">
          <span className="hidden rounded-md bg-muted px-2 py-1 text-[11px] font-medium text-muted-foreground sm:inline-block">
            {roleLabel}
          </span>
          <span className="hidden rounded-md bg-muted px-2 py-1 text-[11px] font-medium text-muted-foreground sm:inline-block">
            v{webConfig.appVersion}
          </span>
          <button
            type="button"
            className="rounded-md px-2 py-1 text-sm text-muted-foreground transition hover:bg-muted hover:text-foreground"
            onClick={() => void handleLogout()}
          >
            退出
          </button>
        </div>
      </div>
    </header>
  );
}
