"use client";

import { useCallback, useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, Copy, LoaderCircle, RefreshCw, Search } from "lucide-react";
import { toast } from "sonner";

import { DateRangeFilter } from "@/components/date-range-filter";
import { ImageLightbox } from "@/components/image-lightbox";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchSystemLogs, type SystemLog } from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";

const LogType = {
  Call: "call",
  Account: "account",
} as const;

const typeLabels: Record<string, string> = {
  [LogType.Call]: "调用日志",
  [LogType.Account]: "账号管理日志",
};

function getDetailText(item: SystemLog, key: string) {
  const value = item.detail?.[key];
  return typeof value === "string" || typeof value === "number" ? String(value) : "-";
}

function formatDuration(item: SystemLog) {
  const value = item.detail?.duration_ms;
  return typeof value === "number" ? `${(value / 1000).toFixed(2)} s` : "-";
}

function getUrls(item: SystemLog | null) {
  const urls = item?.detail?.urls;
  return Array.isArray(urls) ? urls.filter((url): url is string => typeof url === "string") : [];
}

function getStatus(item: SystemLog) {
  const status = item.detail?.status;
  if (status === "success") return "成功";
  if (status === "failed") return "失败";
  return "-";
}

function statusBadgeVariant(item: SystemLog | null) {
  if (item?.detail?.status === "failed") return "danger";
  if (item?.detail?.status === "success") return "success";
  return "secondary";
}

const detailLabels: Record<string, string> = {
  endpoint: "接口",
  model: "模型",
  started_at: "开始时间",
  ended_at: "结束时间",
  duration_ms: "耗时",
  status: "状态",
  key_name: "令牌名称",
  key_role: "角色",
  key_id: "凭据 ID",
  subject_id: "用户 ID",
  provider: "来源",
  error: "错误",
  token: "令牌",
  source: "来源事件",
  added: "新增",
  skipped: "跳过",
  removed: "删除",
};

const primaryDetailKeys = [
  "endpoint",
  "model",
  "status",
  "duration_ms",
  "key_name",
  "key_role",
  "key_id",
  "subject_id",
  "provider",
  "started_at",
  "ended_at",
];

function detailLabel(key: string) {
  return detailLabels[key] || key;
}

function isPrimitiveDetail(value: unknown) {
  return value === null || ["string", "number", "boolean"].includes(typeof value);
}

function formatDetailValue(key: string, value: unknown) {
  if (value === null || value === undefined || value === "") {
    return "—";
  }
  if (key === "duration_ms" && typeof value === "number") {
    return `${(value / 1000).toFixed(2)} s`;
  }
  if (key === "status") {
    if (value === "success") return "成功";
    if (value === "failed") return "失败";
  }
  if (typeof value === "boolean") {
    return value ? "是" : "否";
  }
  return String(value);
}

function getPrimaryDetailEntries(item: SystemLog | null) {
  const detail = item?.detail || {};
  return primaryDetailKeys
    .filter((key) => key in detail && isPrimitiveDetail(detail[key]))
    .map((key) => [key, detail[key]] as const);
}

function getExtraDetailEntries(item: SystemLog | null) {
  const detail = item?.detail || {};
  const skipped = new Set([...primaryDetailKeys, "urls", "error"]);
  return Object.entries(detail).filter(([key, value]) => !skipped.has(key) && isPrimitiveDetail(value));
}

function detailJSON(item: SystemLog | null) {
  return JSON.stringify(item?.detail || {}, null, 2);
}

function LogsContent() {
  const [items, setItems] = useState<SystemLog[]>([]);
  const [type, setType] = useState<string>(LogType.Call);
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [detailLog, setDetailLog] = useState<SystemLog | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [page, setPage] = useState(1);
  const [isLoading, setIsLoading] = useState(true);
  const detailUrls = getUrls(detailLog);
  const detailImages = detailUrls.map((url, index) => ({ id: `${index}`, src: url }));
  const isCallLog = type === LogType.Call;
  const pageSize = 10;
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize));
  const safePage = Math.min(page, pageCount);
  const currentRows = items.slice((safePage - 1) * pageSize, safePage * pageSize);

  const loadLogs = useCallback(async () => {
    setIsLoading(true);
    try {
      const data = await fetchSystemLogs({ type, start_date: startDate, end_date: endDate });
      setItems(data.items);
      setPage(1);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载日志失败");
    } finally {
      setIsLoading(false);
    }
  }, [endDate, startDate, type]);

  const clearFilters = () => {
    setStartDate("");
    setEndDate("");
  };

  const openDetail = (item: SystemLog) => {
    setDetailLog(item);
    setDetailOpen(true);
  };

  const handleCopyDetailJSON = async () => {
    try {
      await navigator.clipboard.writeText(detailJSON(detailLog));
      toast.success("日志详情已复制");
    } catch {
      toast.error("复制失败，请手动复制");
    }
  };

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        eyebrow="Logs"
        title="日志管理"
        actions={
          <>
          <Select value={type} onValueChange={setType}>
            <SelectTrigger className="h-10 w-[150px]"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={LogType.Call}>调用日志</SelectItem>
              <SelectItem value={LogType.Account}>账号管理日志</SelectItem>
            </SelectContent>
          </Select>
          <DateRangeFilter startDate={startDate} endDate={endDate} onChange={(start, end) => { setStartDate(start); setEndDate(end); }} />
          <Button variant="outline" onClick={clearFilters} className="h-10 rounded-lg">
            清除筛选条件
          </Button>
          <Button onClick={() => void loadLogs()} disabled={isLoading} className="h-10 rounded-lg">
            {isLoading ? <LoaderCircle className="size-4 animate-spin" /> : <Search className="size-4" />}
            查询
          </Button>
          </>
        }
      />

      <Card className="overflow-hidden">
        <CardContent className="p-0">
          <div className="flex items-center justify-between border-b border-border px-5 py-4 text-sm text-muted-foreground">
            <span>共 {items.length} 条</span>
            <Button variant="ghost" className="h-8 rounded-lg px-3" onClick={() => void loadLogs()} disabled={isLoading}>
              <RefreshCw className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
              刷新
            </Button>
          </div>
          <div className="overflow-x-auto">
            <Table className="min-w-[820px]">
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>类型</TableHead>
                  {isCallLog ? <TableHead>令牌名称</TableHead> : null}
                  {isCallLog ? <TableHead>调用耗时</TableHead> : null}
                  {isCallLog ? <TableHead>状态</TableHead> : null}
                  <TableHead>简述</TableHead>
                  <TableHead className="w-28">详情</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {currentRows.map((item, index) => (
                  <TableRow key={`${item.time}-${index}`} className="text-muted-foreground">
                    <TableCell className="whitespace-nowrap">{item.time}</TableCell>
                    <TableCell><Badge variant="secondary" className="rounded-md">{typeLabels[item.type] || item.type}</Badge></TableCell>
                    {isCallLog ? <TableCell>{getDetailText(item, "key_name")}</TableCell> : null}
                    {isCallLog ? <TableCell>{formatDuration(item)}</TableCell> : null}
                    {isCallLog ? (
                      <TableCell>
                        <Badge variant={item.detail?.status === "failed" ? "danger" : "success"} className="rounded-md">
                          {getStatus(item)}
                        </Badge>
                      </TableCell>
                    ) : null}
                    <TableCell className="max-w-[420px] truncate text-muted-foreground">{item.summary || "-"}</TableCell>
                    <TableCell>
                      <Button variant="ghost" className="h-8 rounded-lg px-3" onClick={() => openDetail(item)}>
                        查看详情
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3 text-sm text-muted-foreground">
            <span>第 {safePage} / {pageCount} 页，共 {items.length} 条</span>
            <Button variant="outline" size="icon" className="size-9 rounded-lg" disabled={safePage <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>
              <ChevronLeft className="size-4" />
            </Button>
            <Button variant="outline" size="icon" className="size-9 rounded-lg" disabled={safePage >= pageCount} onClick={() => setPage((value) => Math.min(pageCount, value + 1))}>
              <ChevronRight className="size-4" />
            </Button>
          </div>
          {!isLoading && items.length === 0 ? <div className="px-6 py-14 text-center text-sm text-stone-500">没有找到日志</div> : null}
        </CardContent>
      </Card>
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="flex max-h-[90vh] w-[min(94vw,980px)] grid-rows-none flex-col gap-0 overflow-hidden rounded-2xl p-0">
          <DialogHeader className="border-b border-border px-6 py-5 pr-12">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0 space-y-2">
                <DialogTitle>日志详情</DialogTitle>
                <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                  <Badge variant="secondary" className="rounded-md">{detailLog ? typeLabels[detailLog.type] || detailLog.type : "-"}</Badge>
                  <Badge variant={statusBadgeVariant(detailLog)} className="rounded-md">{detailLog ? getStatus(detailLog) : "-"}</Badge>
                  <span>{detailLog?.time || "—"}</span>
                </div>
              </div>
              <Button type="button" variant="outline" className="h-9 rounded-lg px-3" onClick={() => void handleCopyDetailJSON()}>
                <Copy className="size-4" />
                复制 JSON
              </Button>
            </div>
          </DialogHeader>
          <div className="min-h-0 overflow-y-auto px-6 py-5">
            <div className="space-y-5">
              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">摘要</div>
                <div className="rounded-xl border border-border bg-muted/35 p-4">
                  <div className="text-sm font-medium text-foreground">{detailLog?.summary || "—"}</div>
                  <div className="mt-3 grid gap-3 text-sm sm:grid-cols-3">
                    <div>
                      <div className="text-xs text-muted-foreground">令牌</div>
                      <div className="mt-1 truncate font-medium text-foreground">{detailLog ? getDetailText(detailLog, "key_name") : "—"}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">接口</div>
                      <div className="mt-1 truncate font-medium text-foreground">{detailLog ? getDetailText(detailLog, "endpoint") : "—"}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">耗时</div>
                      <div className="mt-1 font-medium text-foreground">{detailLog ? formatDuration(detailLog) : "—"}</div>
                    </div>
                  </div>
                </div>
              </section>

              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">关键字段</div>
                <div className="grid gap-2 md:grid-cols-2">
                  {[...getPrimaryDetailEntries(detailLog), ...getExtraDetailEntries(detailLog)].map(([key, value]) => (
                    <div key={key} className="flex min-w-0 items-start justify-between gap-4 rounded-lg border border-border bg-background px-3 py-2 text-sm">
                      <span className="shrink-0 text-muted-foreground">{detailLabel(key)}</span>
                      <span className="min-w-0 break-words text-right font-medium text-foreground">{formatDetailValue(key, value)}</span>
                    </div>
                  ))}
                  {getPrimaryDetailEntries(detailLog).length === 0 && getExtraDetailEntries(detailLog).length === 0 ? (
                    <div className="rounded-lg border border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground md:col-span-2">
                      没有可展示的字段
                    </div>
                  ) : null}
                </div>
              </section>

              {typeof detailLog?.detail?.error === "string" && detailLog.detail.error ? (
                <section className="space-y-3">
                  <div className="text-sm font-semibold text-foreground">错误信息</div>
                  <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-xl border border-rose-200 bg-rose-50 p-4 text-xs leading-6 text-rose-700 dark:border-rose-800 dark:bg-rose-950/30 dark:text-rose-200">
                    {detailLog.detail.error}
                  </pre>
                </section>
              ) : null}

              {detailUrls.length ? (
                <section className="space-y-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="text-sm font-semibold text-foreground">图片结果</div>
                    <Badge variant="secondary" className="rounded-md">{detailUrls.length} 张</Badge>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
                    {detailUrls.map((url, index) => (
                      <button
                        key={url}
                        type="button"
                        className="group overflow-hidden rounded-xl border border-border bg-muted text-left"
                        onClick={() => {
                          setLightboxIndex(index);
                          setLightboxOpen(true);
                        }}
                      >
                        <div className="aspect-square overflow-hidden bg-muted">
                          <img src={url} alt="" className="h-full w-full object-cover transition duration-200 group-hover:scale-[1.02]" />
                        </div>
                        <div className="truncate border-t border-border px-3 py-2 text-xs text-muted-foreground">{url}</div>
                      </button>
                    ))}
                  </div>
                </section>
              ) : null}

              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">完整 JSON</div>
                <pre className="max-h-72 overflow-auto rounded-xl border border-border bg-muted/40 p-4 text-xs leading-6 text-foreground">
                  {detailJSON(detailLog)}
                </pre>
              </section>
            </div>
          </div>
        </DialogContent>
      </Dialog>
      <ImageLightbox
        images={detailImages}
        currentIndex={lightboxIndex}
        open={lightboxOpen}
        onOpenChange={setLightboxOpen}
        onIndexChange={setLightboxIndex}
      />
    </section>
  );
}

export default function LogsPage() {
  const { isCheckingAuth, session } = useAuthGuard(["admin"]);
  if (isCheckingAuth || !session || session.role !== "admin") {
    return <div className="flex min-h-[40vh] items-center justify-center"><LoaderCircle className="size-5 animate-spin text-stone-400" /></div>;
  }
  return <LogsContent />;
}
