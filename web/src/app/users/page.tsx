"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Ban,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
  UserRound,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  createManagedUser,
  deleteManagedUser,
  fetchManagedRoles,
  fetchManagedUsers,
  updateManagedUser,
  type CreateManagedUserPayload,
  type ManagedUser,
  type ManagedRole,
} from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";

function formatDateTime(value?: string | null) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function normalizeManagedUsers(items: ManagedUser[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

function normalizeManagedRoles(items: ManagedRole[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

type CreateUserForm = {
  username: string;
  name: string;
  password: string;
  confirmPassword: string;
  role_id: string;
  enabled: boolean;
};

type CreateUserErrors = Partial<Record<"username" | "password" | "confirmPassword", string>>;

const accountUsernamePattern = /^[a-z0-9][a-z0-9_.-]{2,31}$/;
const userPageSizeOptions = ["10", "20", "50", "100"];

function createEmptyUserForm(roleId = ""): CreateUserForm {
  return {
    username: "",
    name: "",
    password: "",
    confirmPassword: "",
    role_id: roleId,
    enabled: true,
  };
}

function validateCreateUserForm(values: CreateUserForm) {
  const errors: CreateUserErrors = {};
  const username = values.username.trim().toLowerCase();

  if (!accountUsernamePattern.test(username)) {
    errors.username = "用户名需为 3-32 位小写字母、数字、点、下划线或短横线，并以字母或数字开头";
  }
  if (values.password.length < 8) {
    errors.password = "密码长度不能少于 8 位";
  } else if (values.password.length > 128) {
    errors.password = "密码长度不能超过 128 位";
  }
  if (!values.confirmPassword) {
    errors.confirmPassword = "请确认密码";
  } else if (values.confirmPassword !== values.password) {
    errors.confirmPassword = "两次输入的密码不一致";
  }

  return errors;
}

function createUserPayload(values: CreateUserForm): CreateManagedUserPayload {
  return {
    username: values.username.trim().toLowerCase(),
    name: values.name.trim(),
    password: values.password,
    role_id: values.role_id,
    enabled: values.enabled,
  };
}

function providerLabel(provider?: string) {
  if (provider === "linuxdo") {
    return "Linuxdo";
  }
  if (provider === "local") {
    return "本地";
  }
  return provider || "未知";
}

const linuxDoLevelColors: Record<string, string> = {
  "0": "text-stone-500 dark:text-stone-400",
  "1": "text-emerald-600 dark:text-emerald-400",
  "2": "text-blue-600 dark:text-blue-400",
  "3": "text-amber-600 dark:text-amber-400",
};

function numeric(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

const compactNumberFormatter = new Intl.NumberFormat("zh-CN", {
  maximumFractionDigits: 1,
  notation: "compact",
});

const usageDateFormatter = new Intl.DateTimeFormat("zh-CN", {
  day: "2-digit",
  month: "2-digit",
});

type NormalizedUsagePoint = {
  date: string;
  calls: number;
  success: number;
  failure: number;
  quotaUsed: number;
};

function formatCompactNumber(value: unknown) {
  return compactNumberFormatter.format(numeric(value));
}

function formatUsageDate(value?: string) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return usageDateFormatter.format(date);
}

function normalizeUsageCurve(points?: ManagedUser["usage_curve"]): NormalizedUsagePoint[] {
  if (!Array.isArray(points)) {
    return [];
  }
  return points
    .filter((point) => Boolean(point.date))
    .map((point) => ({
      date: point.date,
      calls: numeric(point.calls),
      success: numeric(point.success),
      failure: numeric(point.failure),
      quotaUsed: numeric(point.quota_used),
    }))
    .sort((left, right) => left.date.localeCompare(right.date));
}

function latestUsagePoint(points?: ManagedUser["usage_curve"]) {
  const safePoints = normalizeUsageCurve(points);
  return safePoints[safePoints.length - 1];
}

function todayQuotaUsed(user: ManagedUser) {
  return latestUsagePoint(user.usage_curve)?.quotaUsed ?? 0;
}

function todayCallCount(user: ManagedUser) {
  return latestUsagePoint(user.usage_curve)?.calls ?? 0;
}

function UsageSparkline({ points }: { points?: ManagedUser["usage_curve"] }) {
  const safePoints = useMemo(() => normalizeUsageCurve(points), [points]);

  if (safePoints.length === 0) {
    return (
      <div
        className="flex h-16 w-[230px] items-center justify-center rounded-lg border border-dashed border-border bg-muted/20 text-xs text-muted-foreground"
        aria-label="调用曲线暂无数据"
      >
        暂无调用
      </div>
    );
  }

  const width = 220;
  const height = 64;
  const paddingX = 8;
  const paddingTop = 8;
  const paddingBottom = 12;
  const baselineY = height - paddingBottom;
  const plotWidth = width - paddingX * 2;
  const plotHeight = height - paddingTop - paddingBottom;
  const calls = safePoints.map((point) => point.calls);
  const maxCalls = Math.max(...calls);
  const minCalls = Math.min(...calls);
  const hasVariation = maxCalls > minCalls;
  const valueRange = Math.max(1, maxCalls - minCalls);
  const chartPoints = safePoints.map((point, index) => {
    const x = safePoints.length === 1 ? width / 2 : paddingX + (index / (safePoints.length - 1)) * plotWidth;
    const y = hasVariation
      ? paddingTop + ((maxCalls - point.calls) / valueRange) * plotHeight
      : maxCalls === 0
        ? baselineY
        : paddingTop + plotHeight / 2;
    return { point, x, y };
  });
  const firstPoint = chartPoints[0];
  const lastPoint = chartPoints[chartPoints.length - 1];
  const linePath = chartPoints.length === 1
    ? `M ${firstPoint.x - 12} ${firstPoint.y} L ${firstPoint.x + 12} ${firstPoint.y}`
    : chartPoints.map(({ x, y }, index) => `${index === 0 ? "M" : "L"} ${x} ${y}`).join(" ");
  const areaPath = chartPoints.length === 1
    ? `M ${firstPoint.x - 12} ${baselineY} L ${firstPoint.x - 12} ${firstPoint.y} L ${firstPoint.x + 12} ${firstPoint.y} L ${firstPoint.x + 12} ${baselineY} Z`
    : `${linePath} L ${lastPoint.x} ${baselineY} L ${firstPoint.x} ${baselineY} Z`;
  const peakPoint = safePoints.reduce((peak, point) => (point.calls > peak.calls ? point : peak), safePoints[0]);
  const latestPoint = lastPoint.point;
  const label = `近 ${safePoints.length} 日调用曲线，今日 ${latestPoint.calls} 次，峰值 ${peakPoint.calls} 次`;

  return (
    <div className="w-[230px] space-y-1.5" aria-label={label}>
      <div className="h-16 overflow-hidden rounded-lg border border-border/70 bg-background">
        <svg viewBox={`0 0 ${width} ${height}`} role="img" className="h-full w-full">
          <title>{label}</title>
          <line x1={paddingX} x2={width - paddingX} y1={paddingTop} y2={paddingTop} className="stroke-border/70" strokeDasharray="3 5" />
          <line x1={paddingX} x2={width - paddingX} y1={baselineY} y2={baselineY} className="stroke-border/70" />
          <path d={areaPath} className="fill-[#3b82f6] opacity-10 dark:opacity-15" />
          <path d={linePath} fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" className="text-[#1456f0] dark:text-sky-300" />
          {chartPoints.map(({ point, x, y }, index) => {
            const isLatest = index === chartPoints.length - 1;
            return (
              <circle
                key={point.date}
                cx={x}
                cy={y}
                r={isLatest ? 3.4 : 2.4}
                className={isLatest ? "fill-[#1456f0] dark:fill-sky-300" : "fill-background stroke-[#1456f0] dark:stroke-sky-300"}
                strokeWidth={isLatest ? 0 : 1.6}
              >
                <title>{`${point.date} 调用 ${point.calls} 次，成功 ${point.success} 次，失败 ${point.failure} 次，额度 ${point.quotaUsed}`}</title>
              </circle>
            );
          })}
        </svg>
      </div>
      <div className="flex items-center justify-between gap-2 text-[11px] leading-4 text-muted-foreground">
        <span>{formatUsageDate(safePoints[0].date)}-{formatUsageDate(latestPoint.date)}</span>
        <span>峰值 {formatCompactNumber(peakPoint.calls)}</span>
      </div>
    </div>
  );
}

function roleLabel(user: ManagedUser, roles: ManagedRole[]) {
  const roleID = String(user.role_id || "").trim();
  const role = roles.find((item) => item.id === roleID);
  return user.role_name || role?.name || "普通用户";
}

function UsersContent() {
  const rolesLoadedRef = useRef(false);
  const [items, setItems] = useState<ManagedUser[]>([]);
  const [roles, setRoles] = useState<ManagedRole[]>([]);
  const [searchText, setSearchText] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState("20");
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(1);
  const [isLoading, setIsLoading] = useState(true);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
  const [createForm, setCreateForm] = useState<CreateUserForm>(() => createEmptyUserForm());
  const [createErrors, setCreateErrors] = useState<CreateUserErrors>({});
  const [isCreating, setIsCreating] = useState(false);
  const [deletingUser, setDeletingUser] = useState<ManagedUser | null>(null);
  const [roleUser, setRoleUser] = useState<ManagedUser | null>(null);
  const [selectedRoleId, setSelectedRoleId] = useState("");
  const [isSavingRole, setIsSavingRole] = useState(false);

  const loadUsers = useCallback(async (overrides: { page?: number; includeRoles?: boolean } = {}) => {
    const requestedPage = overrides.page ?? page;
    const includeRoles = overrides.includeRoles ?? !rolesLoadedRef.current;
    setIsLoading(true);
    try {
      const usersPromise = fetchManagedUsers({
        page: requestedPage,
        page_size: pageSize,
        search: searchText,
        provider: providerFilter,
        status: statusFilter,
      });
      const [usersData, rolesData] = await Promise.all([
        usersPromise,
        includeRoles ? fetchManagedRoles() : Promise.resolve(null),
      ]);
      setItems(normalizeManagedUsers(usersData.items));
      setTotal(Number.isFinite(usersData.total) ? usersData.total : 0);
      setTotalPages(Math.max(1, Number.isFinite(usersData.total_pages) ? usersData.total_pages : 1));
      if (usersData.page && usersData.page !== page) {
        setPage(usersData.page);
      }
      if (rolesData) {
        rolesLoadedRef.current = true;
        const nextRoles = normalizeManagedRoles(rolesData.items);
        setRoles(nextRoles);
        setCreateForm((current) => ({
          ...current,
          role_id: current.role_id || nextRoles[0]?.id || "",
        }));
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载用户失败");
    } finally {
      setIsLoading(false);
    }
  }, [page, pageSize, providerFilter, searchText, statusFilter]);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const safePage = Math.min(page, totalPages);
  const startIndex = total === 0 ? 0 : (safePage - 1) * Number(pageSize) + 1;
  const endIndex = Math.min(safePage * Number(pageSize), total);
  const hasActiveFilters = searchText.trim() !== "" || providerFilter !== "all" || statusFilter !== "all";
  const paginationItems = useMemo(() => {
    const nextItems: (number | "...")[] = [];
    const start = Math.max(1, safePage - 1);
    const end = Math.min(totalPages, safePage + 1);

    if (start > 1) nextItems.push(1);
    if (start > 2) nextItems.push("...");
    for (let current = start; current <= end; current += 1) nextItems.push(current);
    if (end < totalPages - 1) nextItems.push("...");
    if (end < totalPages) nextItems.push(totalPages);

    return nextItems;
  }, [safePage, totalPages]);

  const setItemPending = (id: string, isPending: boolean) => {
    setPendingIds((current) => {
      const next = new Set(current);
      if (isPending) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  };

  const updateCreateField = <Key extends keyof CreateUserForm>(field: Key, value: CreateUserForm[Key]) => {
    setCreateForm((current) => ({ ...current, [field]: value }));
    if (field === "username" || field === "password" || field === "confirmPassword") {
      setCreateErrors((current) => ({ ...current, [field]: undefined }));
    }
  };

  const openCreateDialog = () => {
    const roleId = createForm.role_id || roles[0]?.id || "";
    setCreateForm(createEmptyUserForm(roleId));
    setCreateErrors({});
    setIsCreateDialogOpen(true);
  };

  const closeCreateDialog = (open: boolean) => {
    setIsCreateDialogOpen(open);
    if (!open) {
      setCreateErrors({});
      setCreateForm(createEmptyUserForm(createForm.role_id || roles[0]?.id || ""));
    }
  };

  const handleCreate = async () => {
    const nextErrors = validateCreateUserForm(createForm);
    if (Object.keys(nextErrors).length > 0) {
      setCreateErrors(nextErrors);
      return;
    }

    setIsCreating(true);
    try {
      await createManagedUser(createUserPayload(createForm));
      setCreateForm(createEmptyUserForm(createForm.role_id));
      setCreateErrors({});
      closeCreateDialog(false);
      if (page === 1) {
        await loadUsers({ page: 1, includeRoles: false });
      } else {
        setPage(1);
      }
      toast.success("用户已创建");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建用户失败");
    } finally {
      setIsCreating(false);
    }
  };

  const handleToggle = async (user: ManagedUser) => {
    setItemPending(user.id, true);
    try {
      await updateManagedUser(user.id, { enabled: !user.enabled });
      await loadUsers({ includeRoles: false });
      toast.success(user.enabled ? "用户已禁用" : "用户已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新用户失败");
    } finally {
      setItemPending(user.id, false);
    }
  };

  const openRoleDialog = (user: ManagedUser) => {
    setRoleUser(user);
    setSelectedRoleId(user.role_id || roles[0]?.id || "");
  };

  const handleSaveRole = async () => {
    if (!roleUser || !selectedRoleId) {
      return;
    }
    const user = roleUser;
    setIsSavingRole(true);
    setItemPending(user.id, true);
    try {
      await updateManagedUser(user.id, {
        role_id: selectedRoleId,
      });
      await loadUsers({ includeRoles: false });
      setRoleUser(null);
      toast.success("角色已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存角色失败");
    } finally {
      setIsSavingRole(false);
      setItemPending(user.id, false);
    }
  };

  const handleDelete = async () => {
    if (!deletingUser) {
      return;
    }
    const user = deletingUser;
    setItemPending(user.id, true);
    try {
      await deleteManagedUser(user.id);
      setDeletingUser(null);
      const nextPage = items.length === 1 && page > 1 ? page - 1 : page;
      if (nextPage === page) {
        await loadUsers({ page: nextPage, includeRoles: false });
      } else {
        setPage(nextPage);
      }
      toast.success("用户已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除用户失败");
    } finally {
      setItemPending(user.id, false);
    }
  };

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        eyebrow="Users"
        title="用户管理"
        actions={
          <>
            <Button variant="outline" onClick={() => void loadUsers()} disabled={isLoading} className="h-10 rounded-lg">
              <RefreshCw className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
              刷新
            </Button>
            <Button onClick={openCreateDialog} className="h-10 rounded-lg">
              <Plus className="size-4" />
              创建用户
            </Button>
          </>
        }
      />

      <Card className="overflow-hidden">
        <CardContent className="p-0">
          <div className="flex flex-col gap-3 border-b border-border px-5 py-4">
            <div className="flex items-center justify-between text-sm text-muted-foreground">
              <span>共 {total} 个用户</span>
            </div>
            <div className="grid gap-2 lg:grid-cols-[minmax(18rem,1fr)_160px_160px_auto]">
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={searchText}
                  onChange={(event) => {
                    setSearchText(event.target.value);
                    setPage(1);
                  }}
                  placeholder="搜索用户名、用户 ID、owner 或会话"
                  className="h-10 rounded-lg pl-9"
                />
              </div>
              <Select
                value={providerFilter}
                onValueChange={(value) => {
                  setProviderFilter(value);
                  setPage(1);
                }}
              >
                <SelectTrigger className="h-10 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部来源</SelectItem>
                  <SelectItem value="linuxdo">Linuxdo</SelectItem>
                  <SelectItem value="local">本地</SelectItem>
                </SelectContent>
              </Select>
              <Select
                value={statusFilter}
                onValueChange={(value) => {
                  setStatusFilter(value);
                  setPage(1);
                }}
              >
                <SelectTrigger className="h-10 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部状态</SelectItem>
                  <SelectItem value="enabled">已启用</SelectItem>
                  <SelectItem value="disabled">已禁用</SelectItem>
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="outline"
                className="h-10 rounded-lg px-3"
                disabled={!hasActiveFilters}
                onClick={() => {
                  setSearchText("");
                  setProviderFilter("all");
                  setStatusFilter("all");
                  setPage(1);
                }}
              >
                <X className="size-4" />
                清除
              </Button>
            </div>
          </div>
          <div className="overflow-x-auto">
            <Table className="min-w-[1340px]">
              <TableHeader>
                <TableRow>
                  <TableHead>用户</TableHead>
                  <TableHead>角色</TableHead>
                  <TableHead>来源</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>额度消耗</TableHead>
                  <TableHead className="w-[340px]">调用曲线</TableHead>
                  <TableHead>时间</TableHead>
                  <TableHead className="w-[260px]">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((user) => {
                  const isPending = pendingIds.has(user.id);
                  return (
                    <TableRow key={user.id} className="text-muted-foreground">
                      <TableCell>
                        <div className="min-w-0 space-y-1">
                          <div className="truncate font-medium text-foreground">{user.name || "普通用户"}</div>
                          {user.username ? (
                            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                              <UserRound className="size-3.5" />
                              <span className="truncate">{user.username}</span>
                            </div>
                          ) : null}
                          <code className="block max-w-[260px] truncate font-mono text-xs text-muted-foreground">{user.id}</code>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col items-start gap-1">
                          <Badge variant="secondary" className="rounded-md">
                            {roleLabel(user, roles)}
                          </Badge>
                          <code className="max-w-[160px] truncate font-mono text-xs text-muted-foreground">
                            {user.role_id || "default-user"}
                          </code>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col items-start gap-1.5">
                          <Badge variant={user.provider === "linuxdo" ? "info" : "secondary"} className="rounded-md">
                            {providerLabel(user.provider)}
                            {user.provider === "linuxdo" && (() => {
                              const level = String(user.linuxdo_level || "").trim();
                              return level ? (
                                <span className={`ml-1 ${linuxDoLevelColors[level] || "text-muted-foreground"}`}>· Lv{level}</span>
                              ) : null;
                            })()}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={user.enabled ? "success" : "danger"} className="rounded-md">
                          {user.enabled ? "已启用" : "已禁用"}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1">
                          <div className="text-base font-semibold text-foreground">{numeric(user.quota_used)}</div>
                          <div className="text-xs text-muted-foreground">今日 {todayQuotaUsed(user)}</div>
                        </div>
                      </TableCell>
                      <TableCell className="w-[340px]">
                        <div className="flex items-center gap-4">
                          <UsageSparkline points={user.usage_curve} />
                          <div className="min-w-[72px] space-y-1 text-xs text-muted-foreground">
                            <div>总计 {formatCompactNumber(user.call_count)}</div>
                            <div>今日 {formatCompactNumber(todayCallCount(user))}</div>
                            <div>失败 {formatCompactNumber(user.failure_count)}</div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1 text-xs">
                          <div>创建 {formatDateTime(user.created_at)}</div>
                          <div>使用 {formatDateTime(user.last_used_at)}</div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg px-3"
                            onClick={() => openRoleDialog(user)}
                            disabled={isPending}
                          >
                            <ShieldCheck className="size-4" />
                            角色
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg px-3"
                            onClick={() => void handleToggle(user)}
                            disabled={isPending}
                          >
                            {isPending ? (
                              <LoaderCircle className="size-4 animate-spin" />
                            ) : user.enabled ? (
                              <Ban className="size-4" />
                            ) : (
                              <CheckCircle2 className="size-4" />
                            )}
                            {user.enabled ? "禁用" : "启用"}
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg border-rose-200 px-3 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                            onClick={() => setDeletingUser(user)}
                            disabled={isPending}
                          >
                            <Trash2 className="size-4" />
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          {isLoading ? (
            <div className="flex items-center justify-center py-14">
              <LoaderCircle className="size-5 animate-spin text-stone-400" />
            </div>
          ) : null}
          {!isLoading && items.length === 0 ? <div className="px-6 py-14 text-center text-sm text-stone-500">{hasActiveFilters ? "没有匹配的用户" : "暂无用户"}</div> : null}
          <div className="border-t border-border px-4 py-4">
            <div className="flex items-center justify-center gap-3 overflow-x-auto whitespace-nowrap">
              <div className="shrink-0 text-sm text-muted-foreground">
                显示第 {startIndex} - {endIndex} 条，共 {total} 条
              </div>
              <span className="shrink-0 text-sm leading-none text-muted-foreground">
                {safePage} / {totalPages} 页
              </span>
              <Select
                value={pageSize}
                onValueChange={(value) => {
                  setPageSize(value);
                  setPage(1);
                }}
              >
                <SelectTrigger className="h-10 w-[108px] shrink-0 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {userPageSizeOptions.map((option) => (
                    <SelectItem key={option} value={option}>
                      {option} / 页
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                variant="outline"
                size="icon"
                className="size-10 shrink-0 rounded-lg"
                disabled={safePage <= 1 || isLoading}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
              >
                <ChevronLeft className="size-4" />
              </Button>
              {paginationItems.map((item, index) =>
                item === "..." ? (
                  <span key={`ellipsis-${index}`} className="px-1 text-sm text-muted-foreground">
                    ...
                  </span>
                ) : (
                  <Button
                    key={item}
                    variant={item === safePage ? "default" : "outline"}
                    className="h-10 min-w-10 shrink-0 rounded-lg px-3"
                    disabled={isLoading}
                    onClick={() => setPage(item)}
                  >
                    {item}
                  </Button>
                ),
              )}
              <Button
                variant="outline"
                size="icon"
                className="size-10 shrink-0 rounded-lg"
                disabled={safePage >= totalPages || isLoading}
                onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
              >
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <Dialog open={isCreateDialogOpen} onOpenChange={closeCreateDialog}>
        <DialogContent className="rounded-2xl p-6 sm:max-w-2xl">
          <DialogHeader className="gap-2">
            <DialogTitle>创建用户</DialogTitle>
            <DialogDescription className="text-sm leading-6">创建本地登录用户并绑定角色。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">用户名</label>
              <div className="relative">
                <UserRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={createForm.username}
                  onChange={(event) => updateCreateField("username", event.target.value.toLowerCase())}
                  placeholder="例如：operator_01"
                  autoComplete="username"
                  className="h-11 rounded-xl pl-9"
                  aria-invalid={Boolean(createErrors.username)}
                />
              </div>
              {createErrors.username ? <p className="text-xs leading-5 text-destructive">{createErrors.username}</p> : null}
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">显示名称</label>
              <Input
                value={createForm.name}
                onChange={(event) => updateCreateField("name", event.target.value)}
                placeholder="例如：运营账号"
                className="h-11 rounded-xl"
              />
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">密码</label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={createForm.password}
                  onChange={(event) => updateCreateField("password", event.target.value)}
                  placeholder="至少 8 位"
                  type="password"
                  autoComplete="new-password"
                  className="h-11 rounded-xl pl-9"
                  aria-invalid={Boolean(createErrors.password)}
                />
              </div>
              {createErrors.password ? <p className="text-xs leading-5 text-destructive">{createErrors.password}</p> : null}
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">确认密码</label>
              <Input
                value={createForm.confirmPassword}
                onChange={(event) => updateCreateField("confirmPassword", event.target.value)}
                placeholder="再次输入密码"
                type="password"
                autoComplete="new-password"
                className="h-11 rounded-xl"
                aria-invalid={Boolean(createErrors.confirmPassword)}
              />
              {createErrors.confirmPassword ? <p className="text-xs leading-5 text-destructive">{createErrors.confirmPassword}</p> : null}
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">角色</label>
              <Select value={createForm.role_id} onValueChange={(value) => updateCreateField("role_id", value)}>
                <SelectTrigger className="h-11 rounded-xl">
                  <SelectValue placeholder="选择角色" />
                </SelectTrigger>
                <SelectContent>
                  {roles.map((role) => (
                    <SelectItem key={role.id} value={role.id}>
                      {role.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">状态</label>
              <Select value={createForm.enabled ? "true" : "false"} onValueChange={(value) => updateCreateField("enabled", value === "true")}>
                <SelectTrigger className="h-11 rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="true">已启用</SelectItem>
                  <SelectItem value="false">已禁用</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="secondary" className="h-10 rounded-xl px-5" onClick={() => closeCreateDialog(false)} disabled={isCreating}>
              取消
            </Button>
            <Button type="button" className="h-10 rounded-xl px-5" onClick={() => void handleCreate()} disabled={isCreating}>
              {isCreating ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}
              创建
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(roleUser)} onOpenChange={(open) => (!open ? setRoleUser(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle className="flex items-center gap-2">
              <ShieldCheck className="size-5 text-[#1456f0]" />
              分配角色
            </DialogTitle>
            <DialogDescription className="truncate text-sm">
              {roleUser?.name || "普通用户"} · {roleUser?.id}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <label className="text-sm font-medium text-stone-700 dark:text-foreground">角色</label>
            <Select value={selectedRoleId} onValueChange={setSelectedRoleId}>
              <SelectTrigger className="h-11 rounded-xl">
                <SelectValue placeholder="选择角色" />
              </SelectTrigger>
              <SelectContent>
                {roles.map((role) => (
                  <SelectItem key={role.id} value={role.id}>
                    {role.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl px-5"
              onClick={() => setRoleUser(null)}
              disabled={isSavingRole}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl px-5"
              onClick={() => void handleSaveRole()}
              disabled={isSavingRole || !roleUser || !selectedRoleId}
            >
              {isSavingRole ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldCheck className="size-4" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deletingUser)} onOpenChange={(open) => (!open ? setDeletingUser(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除用户</DialogTitle>
            <DialogDescription className="text-sm leading-6">确认删除「{deletingUser?.name}」吗？</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl px-5"
              onClick={() => setDeletingUser(null)}
              disabled={deletingUser ? pendingIds.has(deletingUser.id) : false}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl bg-rose-600 px-5 text-white hover:bg-rose-700"
              onClick={() => void handleDelete()}
              disabled={deletingUser ? pendingIds.has(deletingUser.id) : false}
            >
              {deletingUser && pendingIds.has(deletingUser.id) ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

export default function UsersPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/users");
  if (isCheckingAuth || !session) {
    return <div className="flex min-h-[40vh] items-center justify-center"><LoaderCircle className="size-5 animate-spin text-stone-400" /></div>;
  }
  return <UsersContent />;
}
