"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Ban,
  CheckCircle2,
  Copy,
  Gauge,
  Eye,
  EyeOff,
  KeyRound,
  LockKeyhole,
  LoaderCircle,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldCheck,
  Trash2,
  UserCircle2,
  UserPen,
} from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  changeProfilePassword,
  deleteProfileAPIKey,
  fetchProfileAPIKey,
  revealProfileAPIKey,
  updateProfileName,
  updateProfileAPIKey,
  upsertProfileAPIKey,
  type UserKey,
} from "@/lib/api";
import { authSessionFromLoginResponse, setVerifiedAuthSession } from "@/lib/session";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { cn } from "@/lib/utils";
import type { StoredAuthSession } from "@/store/auth";

function normalizeProfileKeys(items: UserKey[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

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

function providerLabel(provider?: string) {
  if (provider === "linuxdo") {
    return "Linuxdo";
  }
  if (provider === "local") {
    return "本地账号";
  }
  return provider || "未知";
}

function sessionRoleLabel(session: StoredAuthSession) {
  if (session.role === "admin") {
    return "管理员";
  }
  return session.roleName || "普通用户";
}

function creationConcurrentLimitLabel(session: StoredAuthSession) {
  if (session.role === "admin" || session.creationConcurrentLimit === 0) {
    return "不限制";
  }
  return `${session.creationConcurrentLimit} 个`;
}

function creationRpmLimitLabel(session: StoredAuthSession) {
  if (session.role === "admin" || session.creationRpmLimit === 0) {
    return "不限制";
  }
  return `${session.creationRpmLimit} 次/分`;
}

function billingTypeLabel(session: StoredAuthSession) {
  const billing = session.billing;
  if (!billing || billing.unlimited) {
    return "无限额度";
  }
  return billing.type === "subscription" ? "订阅配额制" : "标准余额制";
}

function billingPrimaryValue(session: StoredAuthSession) {
  const billing = session.billing;
  if (!billing || billing.unlimited) {
    return "不限制";
  }
  if (billing.type === "subscription") {
    return `${billing.available} / ${billing.subscription?.quota_limit ?? 0}`;
  }
  return String(billing.standard?.available_balance ?? billing.available);
}

function billingResetLabel(session: StoredAuthSession) {
  const endsAt = session.billing?.subscription?.quota_period_ends_at;
  return endsAt ? formatDateTime(endsAt) : "—";
}

function maskKey(hasKey: boolean) {
  return hasKey ? "sk-••••••••••••••••••••••••••••••••" : "未生成";
}

type InfoRowProps = {
  label: string;
  value: string;
  code?: boolean;
};

function InfoRow({ label, value, code }: InfoRowProps) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border border-border bg-muted/30 px-3 py-2">
      <span className="text-xs text-muted-foreground">{label}</span>
      {code ? (
        <code className="truncate font-mono text-sm text-foreground">{value || "—"}</code>
      ) : (
        <span className="truncate text-sm font-medium text-foreground">{value || "—"}</span>
      )}
    </div>
  );
}

function ProfileContent({ session }: { session: StoredAuthSession }) {
  const [currentSession, setCurrentSession] = useState(session);
  const [items, setItems] = useState<UserKey[]>([]);
  const [profileName, setProfileName] = useState(session.name || "");
  const [keyName, setKeyName] = useState("我的 API 令牌");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [revealedKey, setRevealedKey] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isSavingProfile, setIsSavingProfile] = useState(false);
  const [isSavingName, setIsSavingName] = useState(false);
  const [isChangingPassword, setIsChangingPassword] = useState(false);
  const [isGenerating, setIsGenerating] = useState(false);
  const [isRevealing, setIsRevealing] = useState(false);
  const [isToggling, setIsToggling] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [isRotateDialogOpen, setIsRotateDialogOpen] = useState(false);
  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);

  const key = items[0] || null;
  const hasKey = Boolean(key);
  const isNameDirty = Boolean(key) && keyName.trim() !== (key?.name || "");
  const isProfileNameDirty = profileName.trim() !== (currentSession.name || "");
  const roleLabel = sessionRoleLabel(currentSession);

  useEffect(() => {
    setCurrentSession(session);
    setProfileName(session.name || "");
  }, [session]);

  const applyItems = useCallback((nextItems: UserKey[]) => {
    const normalized = normalizeProfileKeys(nextItems);
    setItems(normalized);
    setKeyName((currentName) => {
      if (normalized[0]?.name) {
        return normalized[0].name;
      }
      return currentName.trim() || "我的 API 令牌";
    });
  }, []);

  const loadKey = useCallback(async () => {
    setIsLoading(true);
    try {
      const data = await fetchProfileAPIKey();
      applyItems(data.items);
      setRevealedKey("");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载个人密钥失败");
    } finally {
      setIsLoading(false);
    }
  }, [applyItems]);

  useEffect(() => {
    void loadKey();
  }, [loadKey]);

  const handleGenerate = async () => {
    setIsGenerating(true);
    try {
      const data = await upsertProfileAPIKey(keyName.trim());
      applyItems(data.items);
      setRevealedKey(data.key);
      setIsRotateDialogOpen(false);
      toast.success(hasKey ? "密钥已重新生成" : "密钥已生成");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成密钥失败");
    } finally {
      setIsGenerating(false);
    }
  };

  const handleReveal = async () => {
    if (!key) {
      return;
    }
    if (revealedKey) {
      setRevealedKey("");
      return;
    }
    setIsRevealing(true);
    try {
      const data = await revealProfileAPIKey(key.id);
      setRevealedKey(data.key);
      toast.success("密钥已显示");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "查看密钥失败");
    } finally {
      setIsRevealing(false);
    }
  };

  const handleCopy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success("已复制到剪贴板");
    } catch {
      toast.error("复制失败，请手动复制");
    }
  };

  const handleSaveName = async () => {
    if (!key || !isNameDirty) {
      return;
    }
    setIsSavingName(true);
    try {
      const data = await updateProfileAPIKey(key.id, { name: keyName.trim() });
      applyItems(data.items);
      toast.success("名称已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存名称失败");
    } finally {
      setIsSavingName(false);
    }
  };

  const handleSaveProfile = async () => {
    const nextName = profileName.trim();
    if (!nextName) {
      toast.error("昵称不能为空");
      return;
    }
    if (!isProfileNameDirty) {
      return;
    }
    setIsSavingProfile(true);
    try {
      const data = await updateProfileName(nextName);
      const nextSession = authSessionFromLoginResponse(data, currentSession.key);
      await setVerifiedAuthSession(nextSession);
      setCurrentSession(nextSession);
      setProfileName(nextSession.name || "");
      toast.success("昵称已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存昵称失败");
    } finally {
      setIsSavingProfile(false);
    }
  };

  const handleChangePassword = async () => {
    if (!currentPassword) {
      toast.error("请输入当前密码");
      return;
    }
    if (!newPassword) {
      toast.error("请输入新密码");
      return;
    }
    if (newPassword !== confirmPassword) {
      toast.error("两次输入的新密码不一致");
      return;
    }
    setIsChangingPassword(true);
    try {
      await changeProfilePassword(currentPassword, newPassword);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      toast.success("密码已修改");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "修改密码失败");
    } finally {
      setIsChangingPassword(false);
    }
  };

  const handleToggle = async () => {
    if (!key) {
      return;
    }
    setIsToggling(true);
    try {
      const data = await updateProfileAPIKey(key.id, { enabled: !key.enabled });
      applyItems(data.items);
      toast.success(key.enabled ? "密钥已禁用" : "密钥已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新密钥失败");
    } finally {
      setIsToggling(false);
    }
  };

  const handleDelete = async () => {
    if (!key) {
      return;
    }
    setIsDeleting(true);
    try {
      const data = await deleteProfileAPIKey(key.id);
      applyItems(data.items);
      setRevealedKey("");
      setIsDeleteDialogOpen(false);
      toast.success("密钥已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除密钥失败");
    } finally {
      setIsDeleting(false);
    }
  };

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        eyebrow="Profile"
        title="个人中心"
        actions={
          <Button variant="outline" onClick={() => void loadKey()} disabled={isLoading} className="h-10 rounded-lg">
            <RefreshCw className={cn("size-4", isLoading ? "animate-spin" : "")} />
            刷新
          </Button>
        }
      />

      <div className="grid gap-5 xl:grid-cols-[360px_1fr]">
        <div className="flex flex-col gap-5">
          <Card>
            <CardHeader>
              <div className="flex items-center justify-between gap-3">
                <div className="flex min-w-0 items-center gap-3">
                  <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-primary text-primary-foreground">
                    <UserCircle2 className="size-5" />
                  </div>
                  <div className="min-w-0">
                    <CardTitle className="truncate text-lg">{currentSession.name || "用户"}</CardTitle>
                    <CardDescription className="truncate">{currentSession.subjectId || "—"}</CardDescription>
                  </div>
                </div>
                <Badge variant={currentSession.role === "admin" ? "violet" : "secondary"} className="shrink-0 rounded-md">
                  {roleLabel}
                </Badge>
              </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <InfoRow label="用户 ID" value={currentSession.subjectId} code />
              <InfoRow label="登录来源" value={providerLabel(currentSession.provider)} />
              <InfoRow label="角色 ID" value={currentSession.roleId || currentSession.role} code />
              <InfoRow label="创作并发额度" value={creationConcurrentLimitLabel(currentSession)} />
              <InfoRow label="RPM 限制" value={creationRpmLimitLabel(currentSession)} />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300">
                  <Gauge className="size-5" />
                </div>
                <div className="min-w-0">
                  <CardTitle className="text-lg">本地计费</CardTitle>
                  <CardDescription className="truncate">图片计费单位</CardDescription>
                </div>
              </div>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-2 xl:grid-cols-1">
              <InfoRow label="计费类型" value={billingTypeLabel(currentSession)} />
              <InfoRow label={currentSession.billing?.type === "subscription" ? "剩余 / 上限" : "可用余额"} value={billingPrimaryValue(currentSession)} />
              {currentSession.billing?.type === "subscription" && !currentSession.billing.unlimited ? (
                <>
                  <InfoRow label="当期已用" value={String(currentSession.billing.subscription?.quota_used ?? 0)} />
                  <InfoRow label="下次重置" value={billingResetLabel(currentSession)} />
                </>
              ) : null}
              {currentSession.billing?.type === "standard" && !currentSession.billing.unlimited ? (
                <>
                  <InfoRow label="当前余额" value={String(currentSession.billing.standard?.balance ?? 0)} />
                </>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300">
                  <LockKeyhole className="size-5" />
                </div>
                <div className="min-w-0">
                  <CardTitle className="text-lg">登录密码</CardTitle>
                  <CardDescription className="truncate">
                    {currentSession.provider === "local" ? "本地账号" : "外部登录"}
                  </CardDescription>
                </div>
              </div>
            </CardHeader>
            <CardContent>
              {currentSession.provider === "local" ? (
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="profile-current-password">当前密码</FieldLabel>
                    <Input
                      id="profile-current-password"
                      type="password"
                      autoComplete="current-password"
                      value={currentPassword}
                      onChange={(event) => setCurrentPassword(event.target.value)}
                      className="h-10 rounded-lg"
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="profile-new-password">新密码</FieldLabel>
                    <Input
                      id="profile-new-password"
                      type="password"
                      autoComplete="new-password"
                      value={newPassword}
                      onChange={(event) => setNewPassword(event.target.value)}
                      className="h-10 rounded-lg"
                    />
                    <FieldDescription>密码长度不能少于 8 位。</FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="profile-confirm-password">确认新密码</FieldLabel>
                    <Input
                      id="profile-confirm-password"
                      type="password"
                      autoComplete="new-password"
                      value={confirmPassword}
                      onChange={(event) => setConfirmPassword(event.target.value)}
                      className="h-10 rounded-lg"
                    />
                  </Field>
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      className="h-10 rounded-lg"
                      onClick={() => void handleChangePassword()}
                      disabled={isChangingPassword}
                    >
                      {isChangingPassword ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}
                      修改密码
                    </Button>
                  </div>
                </FieldGroup>
              ) : (
                <div className="rounded-xl border border-border bg-muted/30 px-3 py-4 text-sm text-muted-foreground">
                  外部登录账号不使用本地密码。
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        <div className="flex flex-col gap-5">
          <Card>
            <CardHeader>
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300">
                  <UserPen className="size-5" />
                </div>
                <div className="min-w-0">
                  <CardTitle className="text-lg">账号资料</CardTitle>
                  <CardDescription className="truncate">{currentSession.subjectId || "—"}</CardDescription>
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="profile-display-name">昵称</FieldLabel>
                  <div className="flex flex-col gap-2 sm:flex-row">
                    <Input
                      id="profile-display-name"
                      value={profileName}
                      onChange={(event) => setProfileName(event.target.value)}
                      placeholder="昵称"
                      className="h-10 rounded-lg"
                    />
                    <Button
                      type="button"
                      variant="outline"
                      className="h-10 rounded-lg"
                      onClick={() => void handleSaveProfile()}
                      disabled={!isProfileNameDirty || isSavingProfile}
                    >
                      {isSavingProfile ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}
                      保存
                    </Button>
                  </div>
                  <FieldDescription>昵称会显示在导航栏和接口调用记录中。</FieldDescription>
                </Field>
              </FieldGroup>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
            <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300">
                  <KeyRound className="size-5" />
                </div>
                <div className="min-w-0">
                  <CardTitle className="text-lg">接口密钥</CardTitle>
                  <CardDescription className="truncate">
                    {key?.id || "尚未生成"}
                  </CardDescription>
                </div>
              </div>
              {key ? (
                <Badge variant={key.enabled ? "success" : "danger"} className="w-fit rounded-md">
                  {key.enabled ? "已启用" : "已禁用"}
                </Badge>
              ) : (
                <Badge variant="secondary" className="w-fit rounded-md">
                  未生成
                </Badge>
              )}
            </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-5">
            {isLoading ? (
              <div className="flex min-h-[260px] items-center justify-center">
                <LoaderCircle className="size-5 animate-spin text-stone-400" />
              </div>
            ) : (
              <>
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="profile-api-key-name">密钥名称</FieldLabel>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input
                        id="profile-api-key-name"
                        value={keyName}
                        onChange={(event) => setKeyName(event.target.value)}
                        placeholder="我的 API 令牌"
                        className="h-10 rounded-lg"
                      />
                      <Button
                        type="button"
                        variant="outline"
                        className="h-10 rounded-lg"
                        onClick={() => void handleSaveName()}
                        disabled={!key || !isNameDirty || isSavingName}
                      >
                        {isSavingName ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}
                        保存
                      </Button>
                    </div>
                    <FieldDescription>用于区分当前账号的接口调用密钥。</FieldDescription>
                  </Field>
                </FieldGroup>

                <div className="flex min-w-0 flex-col gap-2 rounded-xl border border-border bg-muted/30 p-3">
                  <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
                    <span>Bearer Token</span>
                    <span>更新 {formatDateTime(key?.created_at)}</span>
                  </div>
                  <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
                    <code className="min-w-0 flex-1 truncate rounded-lg bg-background px-3 py-2 font-mono text-sm text-foreground">
                      {revealedKey || maskKey(hasKey)}
                    </code>
                    <div className="flex shrink-0 items-center gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-9 rounded-lg"
                        onClick={() => void handleReveal()}
                        disabled={!key || isRevealing}
                        aria-label={revealedKey ? "隐藏密钥" : "查看密钥"}
                        title={revealedKey ? "隐藏" : "查看"}
                      >
                        {isRevealing ? (
                          <LoaderCircle className="size-4 animate-spin" />
                        ) : revealedKey ? (
                          <EyeOff className="size-4" />
                        ) : (
                          <Eye className="size-4" />
                        )}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-9 rounded-lg"
                        onClick={() => revealedKey ? void handleCopy(revealedKey) : null}
                        disabled={!revealedKey}
                        aria-label="复制密钥"
                        title="复制"
                      >
                        <Copy className="size-4" />
                      </Button>
                    </div>
                  </div>
                </div>

                <div className="grid gap-3 md:grid-cols-3">
                  <InfoRow label="密钥 ID" value={key?.id || "—"} code />
                  <InfoRow label="创建时间" value={formatDateTime(key?.created_at)} />
                  <InfoRow label="最近使用" value={formatDateTime(key?.last_used_at)} />
                </div>

                <div className="flex flex-wrap items-center justify-end gap-2">
                  {key ? (
                    <>
                      <Button
                        type="button"
                        variant="outline"
                        className="h-10 rounded-lg"
                        onClick={() => void handleToggle()}
                        disabled={isToggling}
                      >
                        {isToggling ? (
                          <LoaderCircle className="size-4 animate-spin" />
                        ) : key.enabled ? (
                          <Ban className="size-4" />
                        ) : (
                          <CheckCircle2 className="size-4" />
                        )}
                        {key.enabled ? "禁用" : "启用"}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        className="h-10 rounded-lg"
                        onClick={() => setIsRotateDialogOpen(true)}
                        disabled={isGenerating}
                      >
                        <RotateCcw className="size-4" />
                        重新生成
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        className="h-10 rounded-lg border-rose-200 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                        onClick={() => setIsDeleteDialogOpen(true)}
                        disabled={isDeleting}
                      >
                        <Trash2 className="size-4" />
                        删除
                      </Button>
                    </>
                  ) : (
                    <Button type="button" className="h-10 rounded-lg" onClick={() => void handleGenerate()} disabled={isGenerating}>
                      {isGenerating ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                      生成密钥
                    </Button>
                  )}
                </div>
              </>
            )}
            </CardContent>
          </Card>
        </div>
      </div>

      <Dialog open={isRotateDialogOpen} onOpenChange={setIsRotateDialogOpen}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle className="flex items-center gap-2">
              <ShieldCheck className="size-5 text-[#1456f0]" />
              重新生成密钥
            </DialogTitle>
            <DialogDescription className="text-sm leading-6">
              旧密钥会立即失效
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="secondary" className="h-10 rounded-xl px-5" onClick={() => setIsRotateDialogOpen(false)} disabled={isGenerating}>
              取消
            </Button>
            <Button type="button" className="h-10 rounded-xl px-5" onClick={() => void handleGenerate()} disabled={isGenerating}>
              {isGenerating ? <LoaderCircle className="size-4 animate-spin" /> : <RotateCcw className="size-4" />}
              确认
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={isDeleteDialogOpen} onOpenChange={setIsDeleteDialogOpen}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除密钥</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              确认删除「{key?.name || "我的 API 令牌"}」吗？
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="secondary" className="h-10 rounded-xl px-5" onClick={() => setIsDeleteDialogOpen(false)} disabled={isDeleting}>
              取消
            </Button>
            <Button type="button" variant="destructive" className="h-10 rounded-xl px-5" onClick={() => void handleDelete()} disabled={isDeleting}>
              {isDeleting ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

export default function ProfilePage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/profile");
  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }
  return <ProfileContent session={session} />;
}
