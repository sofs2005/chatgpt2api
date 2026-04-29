"use client";

import { useEffect, useRef } from "react";
import { LoaderCircle } from "lucide-react";
import { Navigate } from "react-router-dom";

import { useAuthGuard } from "@/lib/use-auth-guard";

import { AnnouncementsCard } from "./components/announcements-card";
import { ConfigCard } from "./components/config-card";
import { CPAPoolDialog } from "./components/cpa-pool-dialog";
import { CPAPoolsCard } from "./components/cpa-pools-card";
import { ImportBrowserDialog } from "./components/import-browser-dialog";
import { SettingsHeader } from "./components/settings-header";
import { Sub2APIConnections } from "./components/sub2api-connections";
import { UserKeysCard } from "./components/user-keys-card";
import { useSettingsStore } from "./store";

function SettingsDataController() {
  const didLoadRef = useRef(false);
  const initialize = useSettingsStore((state) => state.initialize);
  const loadPools = useSettingsStore((state) => state.loadPools);
  const pools = useSettingsStore((state) => state.pools);

  useEffect(() => {
    if (didLoadRef.current) {
      return;
    }
    didLoadRef.current = true;
    void initialize();
  }, [initialize]);

  useEffect(() => {
    const hasRunningJobs = pools.some((pool) => {
      const status = pool.import_job?.status;
      return status === "pending" || status === "running";
    });
    if (!hasRunningJobs) {
      return;
    }

    const timer = window.setInterval(() => {
      void loadPools(true);
    }, 1500);
    return () => window.clearInterval(timer);
  }, [loadPools, pools]);

  return null;
}

function AdminSettingsPageContent() {
  return (
    <>
      <SettingsDataController />
      <SettingsHeader />
      <section className="flex flex-col gap-4">
        <ConfigCard />
        <AnnouncementsCard />
        <UserKeysCard />
        <CPAPoolsCard />
        <Sub2APIConnections />
      </section>
      <CPAPoolDialog />
      <ImportBrowserDialog />
    </>
  );
}

function LinuxDoSettingsPageContent() {
  return (
    <>
      <SettingsHeader />
      <section className="flex flex-col gap-4">
        <UserKeysCard />
      </section>
    </>
  );
}

export default function SettingsPage() {
  const { isCheckingAuth, session } = useAuthGuard(["admin", "user"]);

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  if (session.role === "admin") {
    return <AdminSettingsPageContent />;
  }
  if (session.provider === "linuxdo") {
    return <LinuxDoSettingsPageContent />;
  }
  return <Navigate to="/image" replace />;
}
