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
    <header className="sticky top-3 z-40 rounded-[24px] border border-[#f2f3f5] bg-white/92 shadow-[0_0_22.576px_rgba(0,0,0,0.08)] backdrop-blur">
      <div className="flex min-h-14 flex-col gap-2 px-3 py-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-4">
        <div className="flex items-center justify-between gap-3 sm:justify-start">
          <Link
            to="/image"
            className="font-display inline-flex shrink-0 items-center gap-2 py-1 text-[15px] font-semibold text-[#18181b] transition hover:text-[#1456f0]"
          >
            <span className="size-3 rounded-full bg-[#ea5ec1] shadow-[8px_0_0_#1456f0,16px_0_0_#3daeff]" />
            chatgpt2api
          </Link>
          <a
            href="https://github.com/basketikun/chatgpt2api"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-sm text-[#45515e] transition hover:bg-black/[0.05] hover:text-[#18181b]"
            aria-label="GitHub repository"
          >
            <Github className="size-4" />
            <span className="hidden md:inline">GitHub</span>
          </a>
          <button
            type="button"
            className="ml-auto shrink-0 rounded-full px-3 py-1 text-xs font-medium text-[#45515e] transition hover:bg-black/[0.05] hover:text-[#18181b] sm:hidden"
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
                    "relative shrink-0 whitespace-nowrap rounded-full px-3 py-1.5 text-[13px] font-medium transition sm:text-sm",
                    active
                      ? "bg-black/[0.06] text-[#18181b]"
                      : "text-[#45515e] hover:bg-black/[0.05] hover:text-[#18181b]",
                  )
                }
              >
                {item.label}
              </NavLink>
            );
          })}
        </nav>
        <div className="hidden items-center justify-end gap-2 sm:flex sm:gap-3">
          <span className="hidden rounded-full bg-[#f0f0f0] px-2.5 py-1 text-[11px] font-medium text-[#45515e] sm:inline-block">
            {roleLabel}
          </span>
          <span className="hidden rounded-full bg-[#f0f0f0] px-2.5 py-1 text-[11px] font-medium text-[#45515e] sm:inline-block">
            v{webConfig.appVersion}
          </span>
          <button
            type="button"
            className="rounded-full px-3 py-1 text-sm text-[#45515e] transition hover:bg-black/[0.05] hover:text-[#18181b]"
            onClick={() => void handleLogout()}
          >
            退出
          </button>
        </div>
      </div>
    </header>
  );
}
