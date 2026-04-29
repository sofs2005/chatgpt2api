"use client";

import { useEffect, useRef, useState } from "react";
import { Ban, CheckCircle2, Copy, Eye, EyeOff, KeyRound, LoaderCircle, Plus, RefreshCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

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
import { createUserKey, deleteUserKey, fetchUserKeys, revealUserKey, updateUserKey, type UserKey } from "@/lib/api";
import { getStoredAuthSession, type StoredAuthSession } from "@/store/auth";

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

function normalizeUserKeys(items: UserKey[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

export function UserKeysCard() {
  const didLoadRef = useRef(false);
  const [items, setItems] = useState<UserKey[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [isCreating, setIsCreating] = useState(false);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const [revealingIds, setRevealingIds] = useState<Set<string>>(() => new Set());
  const [revealedKeysById, setRevealedKeysById] = useState<Record<string, string>>({});
  const [deletingItem, setDeletingItem] = useState<UserKey | null>(null);
  const [session, setSession] = useState<StoredAuthSession | null>(null);
  const isAdmin = session?.role === "admin";
  const isLinuxDoUser = session?.role === "user" && session.provider === "linuxdo";
  const safeItems = normalizeUserKeys(items);
  const visibleItems = isAdmin ? safeItems : safeItems.slice(0, 1);

  const load = async () => {
    setIsLoading(true);
    try {
      const storedSession = await getStoredAuthSession();
      setSession(storedSession);
      const data = await fetchUserKeys();
      setItems(normalizeUserKeys(data.items));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载用户密钥失败");
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    if (didLoadRef.current) {
      return;
    }
    didLoadRef.current = true;
    void load();
  }, []);

  const handleCreate = async () => {
    const isResetting = isLinuxDoUser && safeItems.length > 0;
    setIsCreating(true);
    try {
      const data = await createUserKey(isAdmin ? name.trim() : "");
      setItems(normalizeUserKeys(data.items));
      setRevealedKeysById((current) => ({ ...current, [data.item.id]: data.key }));
      setName("");
      setIsDialogOpen(false);
      toast.success(isResetting ? "令牌已重置" : isAdmin ? "用户密钥已创建" : "令牌已生成");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : isResetting ? "重置令牌失败" : "创建令牌失败");
    } finally {
      setIsCreating(false);
    }
  };

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

  const setRevealPending = (id: string, isPending: boolean) => {
    setRevealingIds((current) => {
      const next = new Set(current);
      if (isPending) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  };

  const handleToggle = async (item: UserKey) => {
    setItemPending(item.id, true);
    try {
      const data = await updateUserKey(item.id, { enabled: !item.enabled });
      setItems(normalizeUserKeys(data.items));
      toast.success(item.enabled ? "用户密钥已禁用" : "用户密钥已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新用户密钥失败");
    } finally {
      setItemPending(item.id, false);
    }
  };

  const handleDelete = async () => {
    if (!deletingItem) {
      return;
    }
    const item = deletingItem;
    setItemPending(item.id, true);
    try {
      const data = await deleteUserKey(item.id);
      setItems(normalizeUserKeys(data.items));
      setDeletingItem(null);
      setRevealedKeysById((current) => {
        const next = { ...current };
        delete next[item.id];
        return next;
      });
      toast.success("用户密钥已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除用户密钥失败");
    } finally {
      setItemPending(item.id, false);
    }
  };

  const handleReveal = async (item: UserKey) => {
    if (revealedKeysById[item.id]) {
      setRevealedKeysById((current) => {
        const next = { ...current };
        delete next[item.id];
        return next;
      });
      return;
    }

    setRevealPending(item.id, true);
    try {
      const data = await revealUserKey(item.id);
      setRevealedKeysById((current) => ({ ...current, [item.id]: data.key }));
      toast.success("用户密钥已显示");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "查看用户密钥失败");
    } finally {
      setRevealPending(item.id, false);
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

  const handlePrimaryAction = () => {
    if (isAdmin) {
      setIsDialogOpen(true);
      return;
    }
    void handleCreate();
  };

  return (
    <>
      <Card>
        <CardContent className="space-y-6 p-6">
          <div className="flex items-start justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className="flex size-10 items-center justify-center rounded-xl bg-stone-100">
                <KeyRound className="size-5 text-stone-600" />
              </div>
              <div>
                <h2 className="text-lg font-semibold tracking-tight">{isAdmin ? "用户密钥管理" : "我的 API 密钥"}</h2>
                <p className="text-sm text-stone-500">
                  {isAdmin ? "管理员可创建和维护普通用户 API 密钥。" : "每个 Linuxdo 账号仅保留一条 API 令牌，重置后旧令牌立即失效。"}
                </p>
              </div>
            </div>
            <Button className="h-9 rounded-xl bg-stone-950 px-4 text-white hover:bg-stone-800" onClick={handlePrimaryAction} disabled={isCreating}>
              {isCreating ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : isAdmin || visibleItems.length === 0 ? (
                <Plus className="size-4" />
              ) : (
                <RefreshCcw className="size-4" />
              )}
              {isAdmin ? "创建用户密钥" : visibleItems.length > 0 ? "重置令牌" : "生成令牌"}
            </Button>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center py-10">
              <LoaderCircle className="size-5 animate-spin text-stone-400" />
            </div>
          ) : visibleItems.length === 0 ? (
            <div className="rounded-xl bg-stone-50 px-6 py-10 text-center text-sm text-stone-500">
              {isAdmin ? "暂无普通用户密钥。点击右上角按钮后即可创建并分发给其他人。" : "你还没有 API 令牌。点击右上角按钮即可生成。"}
            </div>
          ) : (
            <div className="space-y-3">
              {visibleItems.map((item) => {
                const isPending = pendingIds.has(item.id);
                const isRevealing = revealingIds.has(item.id);
                const revealedKey = revealedKeysById[item.id] ?? "";
                return (
                  <div
                    key={item.id}
                    className="grid gap-3 rounded-xl border border-stone-200 bg-white px-4 py-4 md:grid-cols-[minmax(0,1fr)_minmax(14rem,28rem)_auto] md:items-center"
                  >
                    <div className="min-w-0 space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <div className="truncate text-sm font-medium text-stone-800">{item.name}</div>
                        <Badge variant={item.enabled ? "success" : "secondary"} className="rounded-md">
                          {item.enabled ? "已启用" : "已禁用"}
                        </Badge>
                        {isAdmin && item.owner_name ? (
                          <Badge variant="info" className="rounded-md">
                            {item.owner_name}
                          </Badge>
                        ) : null}
                      </div>
                      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-stone-500">
                        <span>创建时间 {formatDateTime(item.created_at)}</span>
                        <span>最近使用 {formatDateTime(item.last_used_at)}</span>
                      </div>
                    </div>

                    <div className="flex min-w-0 items-center gap-2 rounded-lg border border-stone-200 bg-stone-50 px-3 py-2 text-xs text-stone-500">
                      <span className="shrink-0">{isAdmin ? "密钥" : "令牌"}</span>
                      <code className="min-w-0 flex-1 truncate font-mono text-[12px] text-stone-700">
                        {revealedKey || "••••••••••••••••••••••••"}
                      </code>
                      {revealedKey ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-7 rounded-lg text-stone-500 hover:bg-stone-200 hover:text-stone-800"
                          onClick={() => void handleCopy(revealedKey)}
                          aria-label="复制用户密钥"
                        >
                          <Copy className="size-3.5" />
                        </Button>
                      ) : null}
                    </div>

                    <div className="flex flex-wrap items-center gap-2 md:justify-end">
                      <Button
                        type="button"
                        variant="outline"
                        className="h-9 rounded-xl border-stone-200 bg-white px-4 text-stone-700"
                        onClick={() => void handleReveal(item)}
                        disabled={isRevealing}
                      >
                        {isRevealing ? (
                          <LoaderCircle className="size-4 animate-spin" />
                        ) : revealedKey ? (
                          <EyeOff className="size-4" />
                        ) : (
                          <Eye className="size-4" />
                        )}
                        {revealedKey ? "隐藏" : "查看"}
                      </Button>
                      {isAdmin ? (
                        <>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-9 rounded-xl border-stone-200 bg-white px-4 text-stone-700"
                            onClick={() => void handleToggle(item)}
                            disabled={isPending}
                          >
                            {isPending ? (
                              <LoaderCircle className="size-4 animate-spin" />
                            ) : item.enabled ? (
                              <Ban className="size-4" />
                            ) : (
                              <CheckCircle2 className="size-4" />
                            )}
                            {item.enabled ? "禁用" : "启用"}
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-9 rounded-xl border-rose-200 bg-white px-4 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                            onClick={() => setDeletingItem(item)}
                            disabled={isPending}
                          >
                            {isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                            删除
                          </Button>
                        </>
                      ) : null}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>创建用户密钥</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              可选填写一个备注名称，方便区分不同使用者；创建后可在列表中通过查看按钮再次查看原始密钥。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <label className="text-sm font-medium text-stone-700">名称（可选）</label>
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="例如：设计同学 A、运营临时账号"
              className="h-11 rounded-xl border-stone-200 bg-white"
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setIsDialogOpen(false)}
              disabled={isCreating}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
              onClick={() => void handleCreate()}
              disabled={isCreating}
            >
              {isCreating ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}
              创建
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deletingItem)} onOpenChange={(open) => (!open ? setDeletingItem(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除用户密钥</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              确认删除用户密钥「{deletingItem?.name}」吗？删除后该密钥将无法继续调用接口。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setDeletingItem(null)}
              disabled={deletingItem ? pendingIds.has(deletingItem.id) : false}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl bg-rose-600 px-5 text-white hover:bg-rose-700"
              onClick={() => void handleDelete()}
              disabled={deletingItem ? pendingIds.has(deletingItem.id) : false}
            >
              {deletingItem && pendingIds.has(deletingItem.id) ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
