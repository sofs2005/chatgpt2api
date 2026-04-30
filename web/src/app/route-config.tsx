import type { ReactNode } from "react";

import AccountsPage from "@/app/accounts/page";
import LinuxDoCallbackPage from "@/app/auth/linuxdo/callback/page";
import ImagePage from "@/app/image/page";
import ImageManagerPage from "@/app/image-manager/page";
import HomePage from "@/app/page";
import LoginPage from "@/app/login/page";
import LogsPage from "@/app/logs/page";
import RegisterPage from "@/app/register/page";
import SettingsPage from "@/app/settings/page";
import UsersPage from "@/app/users/page";

export type AppRouteConfig = {
  path: string;
  element: ReactNode;
};

export const appRoutes: AppRouteConfig[] = [
  { path: "/", element: <HomePage /> },
  { path: "/login", element: <LoginPage /> },
  { path: "/auth/linuxdo/callback", element: <LinuxDoCallbackPage /> },
  { path: "/accounts", element: <AccountsPage /> },
  { path: "/register", element: <RegisterPage /> },
  { path: "/image-manager", element: <ImageManagerPage /> },
  { path: "/users", element: <UsersPage /> },
  { path: "/logs", element: <LogsPage /> },
  { path: "/settings", element: <SettingsPage /> },
  { path: "/image", element: <ImagePage /> },
  { path: "*", element: <HomePage /> },
];
