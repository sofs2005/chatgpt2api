"use client";

import { useState } from "react";
import {
  Database,
  HardDrive,
  Image as ImageIcon,
  LoaderCircle,
  RefreshCw,
  Trash2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  SettingsNotice,
  settingsPanelClassName,
} from "./settings-ui";

type CleanupAction = "retention" | "quota" | "thumbnails";

function formatBytes(value?: number) {
  const bytes = Math.max(0, Number(value) || 0);
  if (bytes >= 1024 ** 3) {
    return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
  }
  if (bytes >= 1024 ** 2) {
    return `${(bytes / 1024 ** 2).toFixed(2)} MB`;
  }
  if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${bytes} B`;
}

function formatTime(value?: string) {
  return value && value.trim() ? value : "暂无数据";
}

function StatBlock({
  label,
  value,
  tone = "default",
}: {
  label: string;
  value: string;
  tone?: "default" | "warning";
}) {
  return (
    <div
      className={cn(
        "flex min-h-20 flex-col justify-between rounded-[16px] border bg-background px-4 py-3 shadow-[0_4px_6px_rgba(0,0,0,0.04)]",
        tone === "warning" ? "border-amber-200 bg-amber-50/70" : "border-border/80",
      )}
    >
      <span className="text-xs leading-5 font-medium text-muted-foreground">
        {label}
      </span>
      <span className="truncate text-lg leading-7 font-semibold text-foreground">
        {value}
      </span>
    </div>
  );
}

function UsageBar({
  label,
  value,
  total,
}: {
  label: string;
  value: number;
  total: number;
}) {
  const percent = total > 0 ? Math.min(100, Math.round((value / total) * 100)) : 0;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-3 text-xs leading-5">
        <span className="font-medium text-foreground">{label}</span>
        <span className="shrink-0 text-muted-foreground">{formatBytes(value)}</span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-[#2563eb]"
          style={{ width: `${percent}%` }}
        />
      </div>
    </div>
  );
}

export function ImageStorageGovernanceCard() {
  const [cleanupAction, setCleanupAction] = useState<CleanupAction | null>(null);
  const config = useSettingsStore((state) => state.config);
  const imageStorageGovernance = useSettingsStore(
    (state) => state.imageStorageGovernance,
  );
  const lastImageStorageCleanup = useSettingsStore(
    (state) => state.lastImageStorageCleanup,
  );
  const isLoadingImageStorageGovernance = useSettingsStore(
    (state) => state.isLoadingImageStorageGovernance,
  );
  const isCleaningImageStorage = useSettingsStore(
    (state) => state.isCleaningImageStorage,
  );
  const loadImageStorageGovernance = useSettingsStore(
    (state) => state.loadImageStorageGovernance,
  );
  const cleanupImageStorageByRetention = useSettingsStore(
    (state) => state.cleanupImageStorageByRetention,
  );
  const cleanupImageStorageByQuota = useSettingsStore(
    (state) => state.cleanupImageStorageByQuota,
  );
  const cleanupImageThumbnails = useSettingsStore(
    (state) => state.cleanupImageThumbnails,
  );

  const governance = imageStorageGovernance;
  const totalBytes = governance?.total_bytes ?? 0;
  const limitBytes = governance?.limit_bytes ?? 0;
  const retentionDays = Math.max(1, Number(config?.image_retention_days) || 30);
  const limitMb = Math.max(0, Number(config?.image_storage_limit_mb) || 0);
  const overLimit = (governance?.over_limit_bytes ?? 0) > 0;

  const handleCleanup = async () => {
    if (cleanupAction === "retention") {
      await cleanupImageStorageByRetention();
    } else if (cleanupAction === "quota") {
      await cleanupImageStorageByQuota(false);
    } else if (cleanupAction === "thumbnails") {
      await cleanupImageThumbnails();
    }
    setCleanupAction(null);
  };

  return (
    <SettingsCard
      icon={HardDrive}
      title="图片存储治理"
      description="查看图片、缩略图和参考图占用，并按策略清理缓存文件。"
      tone="slate"
      action={
        <Button
          type="button"
          variant="outline"
          size="lg"
          onClick={() => void loadImageStorageGovernance()}
          disabled={isLoadingImageStorageGovernance}
        >
          {isLoadingImageStorageGovernance ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <RefreshCw data-icon="inline-start" />
          )}
          刷新
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        {isLoadingImageStorageGovernance && !governance ? (
          <div className="flex items-center justify-center rounded-[16px] border border-border/80 bg-background py-10">
            <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <>
            <section className="grid gap-3 sm:grid-cols-3">
              <StatBlock label="总占用" value={formatBytes(totalBytes)} />
              <StatBlock
                label="容量上限"
                value={limitBytes > 0 ? formatBytes(limitBytes) : "未启用"}
                tone={overLimit ? "warning" : "default"}
              />
              <StatBlock
                label="超出容量"
                value={formatBytes(governance?.over_limit_bytes)}
                tone={overLimit ? "warning" : "default"}
              />
            </section>

            <section className={settingsPanelClassName}>
              <div className="flex flex-col gap-3">
                <UsageBar
                  label="原图"
                  value={governance?.images_bytes ?? 0}
                  total={totalBytes}
                />
                <UsageBar
                  label="缩略图"
                  value={governance?.thumbnails_bytes ?? 0}
                  total={totalBytes}
                />
                <UsageBar
                  label="元数据与参考图"
                  value={governance?.metadata_bytes ?? 0}
                  total={totalBytes}
                />
              </div>
            </section>

            <section className="grid gap-3 sm:grid-cols-2">
              <StatBlock
                label="图片数量"
                value={`${governance?.images_count ?? 0} 张`}
              />
              <StatBlock
                label="公开 / 私有"
                value={`${governance?.public_images_count ?? 0} / ${governance?.private_images_count ?? 0}`}
              />
              <StatBlock
                label="参考图附件"
                value={`${governance?.reference_files ?? 0} 个 · ${formatBytes(governance?.reference_bytes)}`}
              />
              <StatBlock
                label="缩略图缓存"
                value={`${governance?.thumbnail_files ?? 0} 个 · ${formatBytes(governance?.thumbnails_bytes)}`}
              />
              <StatBlock
                label="最早图片"
                value={formatTime(governance?.oldest_image_at)}
              />
              <StatBlock
                label="最新图片"
                value={formatTime(governance?.latest_image_at)}
              />
            </section>
          </>
        )}

        <section className="grid gap-2 sm:grid-cols-3">
          <Button
            type="button"
            variant="outline"
            className="min-h-11"
            onClick={() => setCleanupAction("thumbnails")}
            disabled={isCleaningImageStorage || (governance?.thumbnail_files ?? 0) === 0}
          >
            <ImageIcon data-icon="inline-start" />
            清缩略图
          </Button>
          <Button
            type="button"
            variant="outline"
            className="min-h-11"
            onClick={() => setCleanupAction("retention")}
            disabled={isCleaningImageStorage || (governance?.images_count ?? 0) === 0}
          >
            <Trash2 data-icon="inline-start" />
            按天数清理
          </Button>
          <Button
            type="button"
            variant={overLimit ? "destructive" : "outline"}
            className="min-h-11"
            onClick={() => setCleanupAction("quota")}
            disabled={isCleaningImageStorage || limitMb <= 0 || (governance?.images_count ?? 0) === 0}
          >
            <Database data-icon="inline-start" />
            按容量清理
          </Button>
        </section>

        {lastImageStorageCleanup ? (
          <SettingsNotice>
            上次清理删除 {lastImageStorageCleanup.deleted_images} 张图片、{" "}
            {lastImageStorageCleanup.deleted_thumbnails} 个缩略图，释放{" "}
            {formatBytes(lastImageStorageCleanup.deleted_bytes)}。
          </SettingsNotice>
        ) : null}
      </div>

      <Dialog
        open={cleanupAction !== null}
        onOpenChange={(open) => {
          if (!open) setCleanupAction(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认清理图片存储</DialogTitle>
            <DialogDescription>
              {cleanupAction === "thumbnails"
                ? "将删除缩略图缓存，原图和参考图不会被删除。"
                : cleanupAction === "quota"
                  ? "将按容量上限删除最旧的非公开图片，公开图库图片默认保留。"
                  : "将删除保留窗口以前的非公开图片，并同步清理缩略图、元数据和参考图。"}
            </DialogDescription>
          </DialogHeader>
          <div className={settingsPanelClassName}>
            {cleanupAction === "quota"
              ? `当前容量上限为 ${limitMb} MB。`
              : cleanupAction === "retention"
                ? `当前图片保留策略为最近 ${retentionDays} 天。`
                : `当前缩略图缓存占用 ${formatBytes(governance?.thumbnails_bytes)}。`}
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
              disabled={isCleaningImageStorage}
            >
              {isCleaningImageStorage ? (
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
