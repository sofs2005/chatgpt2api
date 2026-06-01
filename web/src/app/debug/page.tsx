"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { cn } from "@/lib/utils";

import { ChatTracePanel } from "./components/chat-panel";
import { SearchProbePanel } from "./components/search-panel";
import { SkillsReferencePanel } from "./components/skill-panel";
import type { DebugTab, DebugTabItem } from "./components/types";

const tabs: DebugTabItem[] = [
  { id: "chat-trace", label: "Chat Trace" },
  { id: "search-probe", label: "Search Probe" },
  { id: "skills-reference", label: "Skills Reference" },
];

function DebugPageContent() {
  const [activeTab, setActiveTab] = useState<DebugTab>("search-probe");

  return (
    <>
      <PageHeader eyebrow="Debug" title="调试台" />
      <section className="mt-5 space-y-4">
        <div className="flex flex-wrap gap-2 rounded-2xl border border-border bg-card p-2">
          {tabs.map((tab) => (
            <Button
              key={tab.id}
              type="button"
              variant="ghost"
              className={cn(
                "rounded-xl",
                activeTab === tab.id ? "bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground" : "",
              )}
              onClick={() => setActiveTab(tab.id)}
            >
              {tab.label}
            </Button>
          ))}
        </div>
        {activeTab === "chat-trace" ? <ChatTracePanel /> : null}
        {activeTab === "search-probe" ? <SearchProbePanel /> : null}
        {activeTab === "skills-reference" ? <SkillsReferencePanel /> : null}
      </section>
    </>
  );
}

export default function DebugPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/debug");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return <DebugPageContent />;
}
