import { Toaster } from "sonner";
import { Route, Routes } from "react-router-dom";

import AccountsPage from "@/app/accounts/page";
import HomePage from "@/app/page";
import ImagePage from "@/app/image/page";
import ImageManagerPage from "@/app/image-manager/page";
import LoginPage from "@/app/login/page";
import LogsPage from "@/app/logs/page";
import RegisterPage from "@/app/register/page";
import SettingsPage from "@/app/settings/page";
import { TopNav } from "@/components/top-nav";

export default function App() {
  return (
    <>
      <Toaster position="top-center" richColors offset={48} />
      <main className="min-h-screen bg-background text-foreground">
        <div className="mx-auto flex min-h-screen max-w-[1440px] flex-col gap-4 px-3 py-3 sm:px-5 lg:px-6">
          <TopNav />
          <Routes>
            <Route path="/" element={<HomePage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/accounts" element={<AccountsPage />} />
            <Route path="/register" element={<RegisterPage />} />
            <Route path="/image-manager" element={<ImageManagerPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/image" element={<ImagePage />} />
            <Route path="*" element={<HomePage />} />
          </Routes>
        </div>
      </main>
    </>
  );
}
