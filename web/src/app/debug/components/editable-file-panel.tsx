"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { LoaderCircle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import {
  fetchEditableFileTasks,
  type EditableFileTask,
  type EditableFileTaskKind,
} from "@/lib/api";
import {
  listEditableFileHistory,
  mergeEditableFileHistoryItems,
  type EditableFileHistoryItem,
  upsertEditableFileHistoryItems,
} from "@/store/editable-file-history";

import { buildEditableFileDownloadHref, collectEditableFilePollIds } from "./editable-file-panel-utils";
import { PptPanel, type EditableFileTaskCreatedHandler } from "./ppt-panel";
import { PsdPanel } from "./psd-panel";

const editableFileKinds: Array<{ id: EditableFileTaskKind; label: string; description: string }> = [
  { id: "ppt", label: "PPT", description: "生成演示文稿" },
  { id: "psd", label: "PSD", description: "生成分层文件" },
];

function getEditableFileKindLabel(kind: EditableFileTaskKind) {
  return editableFileKinds.find((item) => item.id === kind)?.label ?? kind.toUpperCase();
}

function buildHistoryItemFromTask(task: EditableFileTask, existing?: EditableFileHistoryItem): EditableFileHistoryItem {
  const taskId = task.taskId.trim() || task.id.trim();
  if (!existing) {
    return {
      taskId,
      kind: task.kind,
      prompt: "",
      status: task.status,
      createdAt: task.created_at,
      updatedAt: task.updated_at,
      ...(task.result ? { result: task.result } : {}),
      ...(task.error ? { error: task.error } : {}),
      ...(task.logs ? { logs: task.logs } : {}),
    };
  }

  return {
    ...existing,
    taskId,
    kind: task.kind,
    status: task.status,
    updatedAt: task.updated_at,
    ...(task.result ? { result: task.result } : existing.result ? { result: existing.result } : {}),
    ...(task.error ? { error: task.error } : {}),
    ...(task.logs ? { logs: task.logs } : existing.logs ? { logs: existing.logs } : {}),
  };
}

function getTaskStatusClass(status: EditableFileHistoryItem["status"]) {
  switch (status) {
    case "success":
      return "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
    case "error":
      return "border-destructive/20 bg-destructive/10 text-destructive";
    case "running":
      return "border-blue-500/20 bg-blue-500/10 text-blue-700 dark:text-blue-300";
    default:
      return "border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300";
  }
}

function EditableFileHistoryList({ items }: { items: EditableFileHistoryItem[] }) {
  if (items.length === 0) {
    return <div className="rounded-2xl border border-dashed border-border px-4 py-6 text-sm text-muted-foreground">暂无可编辑文件任务历史。</div>;
  }

  return (
    <div className="grid gap-3">
      {items.map((item) => (
        <article key={item.taskId} className="rounded-2xl border border-border bg-muted/25 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="text-sm font-semibold text-foreground">{getEditableFileKindLabel(item.kind)}</h3>
                <span className={cn("inline-flex rounded-full border px-2 py-0.5 text-xs font-medium", getTaskStatusClass(item.status))}>
                  {item.status}
                </span>
              </div>
              <p className="text-sm leading-6 text-muted-foreground">{item.prompt}</p>
            </div>
            <div className="text-xs text-muted-foreground">
              <div>Task ID: {item.taskId}</div>
              <div>更新时间: {item.updatedAt}</div>
            </div>
          </div>

          {item.error ? (
            <div className="mt-3 rounded-xl border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {item.error}
            </div>
          ) : null}

          {item.logs && item.logs.length > 0 ? (
            <div className="mt-3 rounded-xl border border-border bg-background/70 px-3 py-2 text-xs text-muted-foreground">
              <div className="mb-1 font-medium text-foreground">任务日志</div>
              <div className="max-h-40 space-y-1 overflow-auto">
                {item.logs.slice(-12).map((log, index) => (
                  <div key={`${item.taskId}-log-${index}`} className="flex gap-2">
                    <span className="shrink-0 text-muted-foreground/80">{log.time || "--"}</span>
                    <span className="min-w-0 break-words">{log.message}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : null}

          {item.result ? (
            <div className="mt-3 grid gap-2 text-sm">
              {item.result.conversation_id ? <div className="text-muted-foreground">Conversation ID: {item.result.conversation_id}</div> : null}
              <div className="flex flex-wrap gap-2">
                {item.result.primary_path ? (
                  <Button asChild variant="outline" size="sm" className="rounded-xl">
                    <a href={buildEditableFileDownloadHref(item.result.primary_path)} target="_blank" rel="noreferrer">
                      下载主文件
                    </a>
                  </Button>
                ) : null}
                {item.result.zip_path ? (
                  <Button asChild variant="outline" size="sm" className="rounded-xl">
                    <a href={buildEditableFileDownloadHref(item.result.zip_path)} target="_blank" rel="noreferrer">
                      下载 ZIP
                    </a>
                  </Button>
                ) : null}
              </div>
            </div>
          ) : null}
        </article>
      ))}
    </div>
  );
}

export function EditableFilePanel() {
  const [activeKind, setActiveKind] = useState<EditableFileTaskKind>("ppt");
  const [history, setHistory] = useState<EditableFileHistoryItem[]>([]);
  const [isLoadingHistory, setIsLoadingHistory] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [pollNotice, setPollNotice] = useState("");
  const historyRef = useRef<EditableFileHistoryItem[]>([]);

  const activeTaskIds = useMemo(() => collectEditableFilePollIds(history), [history]);
  const activeTaskIdsKey = activeTaskIds.join("|");

  useEffect(() => {
    let cancelled = false;

    const loadHistory = async () => {
      try {
        const items = await listEditableFileHistory();
        if (cancelled) {
          return;
        }
        setHistory(items);
        historyRef.current = items;
      } catch (error) {
        if (!cancelled) {
          setLoadError(error instanceof Error ? error.message : "加载可编辑文件历史失败");
        }
      } finally {
        if (!cancelled) {
          setIsLoadingHistory(false);
        }
      }
    };

    void loadHistory();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    historyRef.current = history;
  }, [history]);

  useEffect(() => {
    if (activeTaskIds.length === 0) {
      setPollNotice("");
      return;
    }

    let cancelled = false;
    let inFlight = false;

    const poll = async () => {
      if (cancelled || inFlight) {
        return;
      }
      inFlight = true;
      try {
        const taskList = await fetchEditableFileTasks(activeTaskIds);
        if (cancelled) {
          return;
        }

        setPollNotice(
          taskList.missing_ids.length > 0 ? `有 ${taskList.missing_ids.length} 个任务暂时缺失，已跳过本轮轮询。` : "",
        );

        if (taskList.items.length > 0) {
          const currentHistory = historyRef.current;
          const updates = taskList.items
            .map((task) => {
              const taskId = task.taskId.trim() || task.id.trim();
              const existing = currentHistory.find((item) => item.taskId === taskId);
              if (!existing) {
                return null;
              }
              return buildHistoryItemFromTask(task, existing);
            })
            .filter((item): item is EditableFileHistoryItem => Boolean(item));

          if (updates.length > 0) {
            setHistory((current) => mergeEditableFileHistoryItems(current, updates));
            void upsertEditableFileHistoryItems(updates);
          }
        }
      } catch (error) {
        if (!cancelled) {
          setPollNotice(error instanceof Error ? `轮询任务状态失败：${error.message}` : "轮询任务状态失败");
        }
      } finally {
        inFlight = false;
      }
    };

    void poll();
    const timer = window.setInterval(() => {
      void poll();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [activeTaskIdsKey]);

  const handleTaskCreated: EditableFileTaskCreatedHandler = async (task, prompt, clientTaskId) => {
    const nextItem = buildHistoryItemFromTask(task, {
      taskId: task.taskId.trim() || task.id.trim(),
      kind: task.kind,
      prompt,
      status: task.status,
      createdAt: task.created_at,
      updatedAt: task.updated_at,
      clientTaskId,
      ...(task.result ? { result: task.result } : {}),
      ...(task.error ? { error: task.error } : {}),
      ...(task.logs ? { logs: task.logs } : {}),
    });

    setHistory((current) => mergeEditableFileHistoryItems(current, [nextItem]));
    await upsertEditableFileHistoryItems([nextItem]);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Editable Files</CardTitle>
        <CardDescription>PPT 与 PSD 生成任务的调试面板。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="flex flex-wrap gap-2 rounded-2xl border border-border bg-muted/25 p-2">
          {editableFileKinds.map((kind) => (
            <Button
              key={kind.id}
              type="button"
              variant={activeKind === kind.id ? "default" : "ghost"}
              className={cn("rounded-xl", activeKind === kind.id ? "bg-primary text-primary-foreground hover:bg-primary/90" : "")}
              onClick={() => setActiveKind(kind.id)}
            >
              <span className="flex flex-col items-start">
                <span>{kind.label}</span>
                <span className="text-[11px] font-normal opacity-80">{kind.description}</span>
              </span>
            </Button>
          ))}
        </div>

        {pollNotice ? (
          <div className="rounded-xl border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-300">
            {pollNotice}
          </div>
        ) : null}

        {loadError ? (
          <div className="rounded-xl border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">{loadError}</div>
        ) : null}

        <div className="rounded-2xl border border-border bg-background p-4">
          {activeKind === "ppt" ? <PptPanel onTaskCreated={handleTaskCreated} /> : <PsdPanel onTaskCreated={handleTaskCreated} />}
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <h3 className="text-sm font-semibold text-foreground">任务历史</h3>
            {isLoadingHistory ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <LoaderCircle className="size-4 animate-spin" />
                加载中
              </div>
            ) : (
              <div className="text-xs text-muted-foreground">共 {history.length} 条</div>
            )}
          </div>
          <EditableFileHistoryList items={history} />
        </div>
      </CardContent>
    </Card>
  );
}
