"use client";

import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { cn } from "@/lib/utils";

type SettingsCardTone = "blue" | "amber" | "slate" | "violet";

const toneClassNames: Record<SettingsCardTone, string> = {
  amber: "bg-amber-50 text-amber-700 ring-1 ring-amber-100",
  blue: "bg-[#edf4ff] text-[#1456f0] ring-1 ring-blue-100",
  slate: "bg-secondary text-muted-foreground ring-1 ring-border",
  violet: "bg-violet-50 text-violet-700 ring-1 ring-violet-100",
};

type SettingsCardProps = {
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
  description: string;
  icon: LucideIcon;
  meta?: ReactNode;
  title: string;
  tone?: SettingsCardTone;
};

export const settingsInputClassName = "bg-background";
export const settingsDialogInputClassName = "h-11 bg-background";
export const settingsInlineCodeClassName =
  "min-w-0 break-all rounded-[13px] bg-muted px-2.5 py-1.5 font-mono text-xs leading-5 text-muted-foreground";
export const settingsListItemClassName =
  "rounded-[16px] border border-border/80 bg-background px-4 py-4 shadow-[0_4px_6px_rgba(0,0,0,0.04)]";
export const settingsPanelClassName =
  "rounded-[16px] border border-[#f2f3f5] bg-muted/30 p-4";
export const settingsToggleClassName =
  "flex min-h-14 items-center gap-3 rounded-[16px] border border-border/80 bg-background px-4 py-3 text-sm font-medium text-foreground shadow-[0_4px_6px_rgba(0,0,0,0.04)]";
export const settingsInlineToggleClassName =
  "flex min-h-11 items-center gap-3 rounded-[13px] border border-[#f2f3f5] bg-background/70 px-3 py-2.5 text-sm font-medium text-foreground";

export function SettingsCard({
  action,
  children,
  className,
  contentClassName,
  icon: Icon,
  meta,
  title,
  tone = "blue",
}: SettingsCardProps) {
  return (
    <Card
      className={cn(
        "overflow-hidden rounded-[20px] border-[#f2f3f5]",
        className,
      )}
    >
      <CardHeader className="gap-4 p-5 pb-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <div
            className={cn(
              "flex size-10 shrink-0 items-center justify-center rounded-[13px]",
              toneClassNames[tone],
            )}
          >
            <Icon className="size-5" />
          </div>
          <div className="min-w-0">
            <CardTitle className="text-lg leading-7 font-semibold">
              {title}
            </CardTitle>
          </div>
        </div>
        {meta || action ? (
          <div className="flex shrink-0 flex-wrap items-center gap-2 sm:justify-end">
            {meta}
            {action}
          </div>
        ) : null}
      </CardHeader>
      <CardContent className={cn("p-5 sm:p-6", contentClassName)}>
        {children}
      </CardContent>
    </Card>
  );
}

export function SettingsNotice({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "rounded-[16px] border border-[#f2f3f5] bg-muted/60 px-4 py-3 text-sm leading-6 text-muted-foreground",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function SettingsEmptyState({
  description,
  icon: Icon,
  title,
}: {
  description: string;
  icon: LucideIcon;
  title: string;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-[20px] border border-[#f2f3f5] bg-muted/55 px-6 py-10 text-center">
      <Icon className="size-8 text-muted-foreground/45" />
      <div className="flex flex-col gap-1">
        <p className="text-sm font-medium text-foreground">{title}</p>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}
