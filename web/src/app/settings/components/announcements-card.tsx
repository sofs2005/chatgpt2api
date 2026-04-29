"use client";

import { useEffect, useRef, useState } from "react";
import { Edit3, LoaderCircle, Megaphone, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  createAnnouncement,
  deleteAnnouncement,
  fetchAdminAnnouncements,
  updateAnnouncement,
  type Announcement,
} from "@/lib/api";

type AnnouncementForm = {
  title: string;
  content: string;
  enabled: boolean;
  show_login: boolean;
  show_image: boolean;
};

const emptyForm: AnnouncementForm = {
  title: "",
  content: "",
  enabled: true,
  show_login: true,
  show_image: false,
};

function formatDateTime(value?: string | null) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function formFromAnnouncement(item: Announcement): AnnouncementForm {
  return {
    title: item.title || "",
    content: item.content || "",
    enabled: Boolean(item.enabled),
    show_login: Boolean(item.show_login),
    show_image: Boolean(item.show_image),
  };
}

export function AnnouncementsCard() {
  const didLoadRef = useRef(false);
  const [items, setItems] = useState<Announcement[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<Announcement | null>(null);
  const [deletingItem, setDeletingItem] = useState<Announcement | null>(null);
  const [form, setForm] = useState<AnnouncementForm>(emptyForm);

  const load = async () => {
    setIsLoading(true);
    try {
      const data = await fetchAdminAnnouncements();
      setItems(data.items);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载公告失败");
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

  const openCreateDialog = () => {
    setEditingItem(null);
    setForm(emptyForm);
    setDialogOpen(true);
  };

  const openEditDialog = (item: Announcement) => {
    setEditingItem(item);
    setForm(formFromAnnouncement(item));
    setDialogOpen(true);
  };

  const updateForm = (updates: Partial<AnnouncementForm>) => {
    setForm((current) => ({ ...current, ...updates }));
  };

  const handleSave = async () => {
    const payload = {
      title: form.title.trim(),
      content: form.content.trim(),
      enabled: form.enabled,
      show_login: form.show_login,
      show_image: form.show_image,
    };
    if (!payload.content) {
      toast.error("请输入公告内容");
      return;
    }
    if (!payload.show_login && !payload.show_image) {
      toast.error("请选择至少一个展示位置");
      return;
    }

    setIsSaving(true);
    try {
      const data = editingItem
        ? await updateAnnouncement(editingItem.id, payload)
        : await createAnnouncement(payload);
      setItems(data.items);
      setDialogOpen(false);
      setEditingItem(null);
      setForm(emptyForm);
      toast.success(editingItem ? "公告已更新" : "公告已创建");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存公告失败");
    } finally {
      setIsSaving(false);
    }
  };

  const handleToggleEnabled = async (item: Announcement) => {
    setItemPending(item.id, true);
    try {
      const data = await updateAnnouncement(item.id, { enabled: !item.enabled });
      setItems(data.items);
      toast.success(item.enabled ? "公告已停用" : "公告已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新公告失败");
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
      const data = await deleteAnnouncement(item.id);
      setItems(data.items);
      setDeletingItem(null);
      toast.success("公告已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除公告失败");
    } finally {
      setItemPending(item.id, false);
    }
  };

  return (
    <>
      <Card>
        <CardContent className="flex flex-col gap-6 p-6">
          <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
            <div className="flex items-center gap-3">
              <div className="flex size-10 items-center justify-center rounded-xl bg-amber-100">
                <Megaphone className="size-5 text-amber-800" />
              </div>
              <div>
                <h2 className="text-lg font-semibold tracking-tight">公告管理</h2>
                <p className="text-sm text-stone-500">创建多条公告，并分别选择显示在登录页或画图主页。</p>
              </div>
            </div>
            <Button className="h-9 rounded-xl bg-stone-950 px-4 text-white hover:bg-stone-800" onClick={openCreateDialog}>
              <Plus className="size-4" />
              添加公告
            </Button>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center py-10">
              <LoaderCircle className="size-5 animate-spin text-stone-400" />
            </div>
          ) : items.length === 0 ? (
            <div className="rounded-xl bg-stone-50 px-6 py-10 text-center text-sm text-stone-500">
              暂无公告。添加后可选择在登录页、画图主页或两个位置同时展示。
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {items.map((item) => {
                const isPending = pendingIds.has(item.id);
                return (
                  <div key={item.id} className="flex flex-col gap-4 rounded-xl border border-stone-200 bg-white px-4 py-4 lg:flex-row lg:items-center lg:justify-between">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <div className="truncate text-sm font-semibold text-stone-900">{item.title || "公告"}</div>
                        <Badge variant={item.enabled ? "success" : "secondary"} className="rounded-md">
                          {item.enabled ? "已启用" : "已停用"}
                        </Badge>
                        {item.show_login ? <Badge variant="warning" className="rounded-md">登录页</Badge> : null}
                        {item.show_image ? <Badge variant="info" className="rounded-md">画图主页</Badge> : null}
                      </div>
                      <p className="mt-2 line-clamp-2 whitespace-pre-wrap break-words text-sm leading-6 text-stone-600">
                        {item.content}
                      </p>
                      <div className="mt-2 text-xs text-stone-400">更新于 {formatDateTime(item.updated_at)}</div>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        className="h-9 rounded-xl border-stone-200 bg-white px-4 text-stone-700"
                        onClick={() => void handleToggleEnabled(item)}
                        disabled={isPending}
                      >
                        {isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}
                        {item.enabled ? "停用" : "启用"}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        className="h-9 rounded-xl border-stone-200 bg-white px-4 text-stone-700"
                        onClick={() => openEditDialog(item)}
                        disabled={isPending}
                      >
                        <Edit3 className="size-4" />
                        编辑
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        className="h-9 rounded-xl border-rose-200 bg-white px-4 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                        onClick={() => setDeletingItem(item)}
                        disabled={isPending}
                      >
                        <Trash2 className="size-4" />
                        删除
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>{editingItem ? "编辑公告" : "添加公告"}</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              公告内容会按勾选位置显示给对应页面的用户。
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-stone-700">标题</label>
              <Input
                value={form.title}
                onChange={(event) => updateForm({ title: event.target.value })}
                placeholder="公告"
                className="h-11 rounded-xl border-stone-200 bg-white"
              />
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-stone-700">内容</label>
              <Textarea
                value={form.content}
                onChange={(event) => updateForm({ content: event.target.value })}
                placeholder="填写公告内容"
                className="min-h-36 rounded-xl border-stone-200 bg-white"
              />
            </div>
            <div className="grid gap-3 md:grid-cols-3">
              <label className="flex items-center gap-2 rounded-xl border border-stone-200 bg-white px-3 py-3 text-sm text-stone-700">
                <Checkbox checked={form.enabled} onCheckedChange={(checked) => updateForm({ enabled: Boolean(checked) })} />
                启用公告
              </label>
              <label className="flex items-center gap-2 rounded-xl border border-stone-200 bg-white px-3 py-3 text-sm text-stone-700">
                <Checkbox checked={form.show_login} onCheckedChange={(checked) => updateForm({ show_login: Boolean(checked) })} />
                登录页显示
              </label>
              <label className="flex items-center gap-2 rounded-xl border border-stone-200 bg-white px-3 py-3 text-sm text-stone-700">
                <Checkbox checked={form.show_image} onCheckedChange={(checked) => updateForm({ show_image: Boolean(checked) })} />
                画图主页显示
              </label>
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setDialogOpen(false)}
              disabled={isSaving}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
              onClick={() => void handleSave()}
              disabled={isSaving}
            >
              {isSaving ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deletingItem)} onOpenChange={(open) => (!open ? setDeletingItem(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除公告</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              确认删除公告「{deletingItem?.title || "公告"}」吗？删除后不会再显示在任何页面。
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
