"use client";

import { useState } from "react";
import {
  LoaderCircle,
  RefreshCw,
  Save,
  ScrollText,
  Trash2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  SettingsNotice,
  settingsInputClassName,
  settingsPanelClassName,
} from "./settings-ui";

const LOG_LEVEL_OPTIONS = ["debug", "info", "warning", "error"];

function formatLogTime(value?: string) {
  return value && value.trim() ? value : "暂无数据";
}

function StatBlock({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-h-20 flex-col justify-between rounded-[16px] border border-border/80 bg-background px-4 py-3 shadow-[0_4px_6px_rgba(0,0,0,0.04)]">
      <span className="text-xs leading-5 font-medium text-muted-foreground">
        {label}
      </span>
      <span className="truncate text-lg leading-7 font-semibold text-foreground">
        {value}
      </span>
    </div>
  );
}

function LogLevelOption({
  checked,
  label,
  onCheckedChange,
}: {
  checked: boolean;
  label: string;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
      <Checkbox
        checked={checked}
        onCheckedChange={(value) => onCheckedChange(Boolean(value))}
      />
      <span className="min-w-0 leading-5">{label}</span>
    </label>
  );
}

export function LogGovernanceCard() {
  const [cleanupDialogOpen, setCleanupDialogOpen] = useState(false);
  const config = useSettingsStore((state) => state.config);
  const isLoadingConfig = useSettingsStore((state) => state.isLoadingConfig);
  const isSavingConfig = useSettingsStore((state) => state.isSavingConfig);
  const logGovernance = useSettingsStore((state) => state.logGovernance);
  const lastLogCleanup = useSettingsStore((state) => state.lastLogCleanup);
  const isLoadingLogGovernance = useSettingsStore(
    (state) => state.isLoadingLogGovernance,
  );
  const isCleaningLogs = useSettingsStore((state) => state.isCleaningLogs);
  const setLogRetentionDays = useSettingsStore(
    (state) => state.setLogRetentionDays,
  );
  const setLogLevel = useSettingsStore((state) => state.setLogLevel);
  const saveConfig = useSettingsStore((state) => state.saveConfig);
  const loadLogGovernance = useSettingsStore((state) => state.loadLogGovernance);
  const cleanupLogsByRetention = useSettingsStore(
    (state) => state.cleanupLogsByRetention,
  );

  const retentionDays = Math.max(1, Number(config?.log_retention_days) || 7);
  const total = logGovernance?.total ?? 0;

  const handleCleanup = async () => {
    await cleanupLogsByRetention();
    setCleanupDialogOpen(false);
  };

  if (isLoadingConfig) {
    return (
      <SettingsCard
        icon={ScrollText}
        title="日志数据治理"
        description="配置日志保留周期、级别和历史数据清理。"
        tone="amber"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard
      icon={ScrollText}
      title="日志数据治理"
      description="配置日志保留周期、级别和历史数据清理。"
      tone="amber"
      action={
        <Button
          size="lg"
          onClick={() => void saveConfig()}
          disabled={isSavingConfig}
        >
          {isSavingConfig ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <Save data-icon="inline-start" />
          )}
          保存
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        <section className="flex flex-col gap-3">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
            <h3 className="truncate text-sm leading-6 font-semibold text-foreground">
              保留策略
            </h3>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="w-full sm:w-auto"
              onClick={() => void loadLogGovernance()}
              disabled={isLoadingLogGovernance}
            >
              {isLoadingLogGovernance ? (
                <LoaderCircle data-icon="inline-start" className="animate-spin" />
              ) : (
                <RefreshCw data-icon="inline-start" />
              )}
              刷新统计
            </Button>
          </div>
          <Field className="gap-1.5">
            <FieldLabel htmlFor="settings-log-retention-days">
              日志保留天数
            </FieldLabel>
            <Input
              id="settings-log-retention-days"
              type="number"
              min={1}
              max={3650}
              value={String(config?.log_retention_days || "")}
              onChange={(event) => setLogRetentionDays(event.target.value)}
              placeholder="7"
              className={settingsInputClassName}
            />
            <FieldDescription>
              按保留策略清理时会保留最近 N 天日志，删除更早的历史日志。
            </FieldDescription>
          </Field>
        </section>

        <section className="flex flex-col gap-3">
          <h3 className="truncate text-sm leading-6 font-semibold text-foreground">
            控制台日志级别
          </h3>
          <div className="grid grid-cols-2 gap-2">
            {LOG_LEVEL_OPTIONS.map((level) => (
              <LogLevelOption
                key={level}
                checked={Boolean(config?.log_levels?.includes(level))}
                onCheckedChange={(checked) => setLogLevel(level, checked)}
                label={level.charAt(0).toUpperCase() + level.slice(1)}
              />
            ))}
          </div>
          <SettingsNotice>
            不选择时默认记录 info、warning 和 error；开启 debug 后日志量会明显增加。
          </SettingsNotice>
        </section>

        <section className="flex flex-col gap-3">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
            <h3 className="truncate text-sm leading-6 font-semibold text-foreground">
              数据概览
            </h3>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="w-full sm:w-auto"
              onClick={() => setCleanupDialogOpen(true)}
              disabled={isCleaningLogs || total === 0}
            >
              {isCleaningLogs ? (
                <LoaderCircle data-icon="inline-start" className="animate-spin" />
              ) : (
                <Trash2 data-icon="inline-start" />
              )}
              按策略清理
            </Button>
          </div>
          {isLoadingLogGovernance ? (
            <div className="flex items-center justify-center rounded-[16px] border border-border/80 bg-background py-8">
              <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-3">
              <StatBlock label="日志总量" value={String(total)} />
              <StatBlock
                label="最早日志"
                value={formatLogTime(logGovernance?.oldest_time)}
              />
              <StatBlock
                label="最新日志"
                value={formatLogTime(logGovernance?.latest_time)}
              />
            </div>
          )}
          {lastLogCleanup ? (
            <SettingsNotice>
              上次清理删除 {lastLogCleanup.deleted} 条，保留自{" "}
              {lastLogCleanup.cutoff_date} 起的最近日志。
            </SettingsNotice>
          ) : null}
        </section>
      </div>

      <Dialog open={cleanupDialogOpen} onOpenChange={setCleanupDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>清理历史日志</DialogTitle>
            <DialogDescription>
              将删除保留窗口以前的日志记录，此操作不会删除图片文件或账号数据。
            </DialogDescription>
          </DialogHeader>
          <div className={settingsPanelClassName}>
            当前保留策略为最近 {retentionDays} 天。确认后会清理更早的历史日志。
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline">
                取消
              </Button>
            </DialogClose>
            <Button
              type="button"
              variant="destructive"
              onClick={() => void handleCleanup()}
              disabled={isCleaningLogs}
            >
              {isCleaningLogs ? (
                <LoaderCircle data-icon="inline-start" className="animate-spin" />
              ) : (
                <Trash2 data-icon="inline-start" />
              )}
              确认清理
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  );
}
