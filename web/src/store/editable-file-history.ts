"use client";

import localforage from "localforage";

import {
  type EditableFileTaskKind,
  type EditableFileTaskResult,
  type EditableFileTaskStatus,
} from "@/lib/api";
import { getStoredAuthSession, type StoredAuthSession } from "@/store/auth";

export type EditableFileHistoryItem = {
  taskId: string;
  kind: EditableFileTaskKind;
  prompt: string;
  status: EditableFileTaskStatus;
  createdAt: string;
  updatedAt: string;
  clientTaskId?: string;
  result?: EditableFileTaskResult;
  error?: string;
};

const editableFileHistoryStorage = localforage.createInstance({
  name: "chatgpt2api",
  storeName: "editable_file_history",
});

const EDITABLE_FILE_HISTORY_KEY_PREFIX = "items";
let editableFileHistoryWriteQueue: Promise<void> = Promise.resolve();

function editableFileHistoryScopeFromSession(session: StoredAuthSession | null) {
  if (!session) {
    return "anonymous";
  }
  const sessionKey = session.key.trim();
  const subjectId = session.subjectId.trim();
  const scope = `${session.provider || "local"}:${session.role}:${subjectId || "unknown"}`;
  return sessionKey ? `${sessionKey}:${scope}` : scope;
}

export function buildEditableFileHistoryStorageKey(session: StoredAuthSession | null | undefined) {
  return `${EDITABLE_FILE_HISTORY_KEY_PREFIX}:${editableFileHistoryScopeFromSession(session || null)}`;
}

const EDITABLE_FILE_TASK_KIND_VALUES = ["ppt", "psd"] as const satisfies readonly EditableFileTaskKind[];
const EDITABLE_FILE_TASK_STATUS_VALUES = ["queued", "running", "success", "error"] as const satisfies readonly EditableFileTaskStatus[];

function getTimestamp(value: string) {
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

function isEditableFileTaskKind(value: unknown): value is EditableFileTaskKind {
  return typeof value === "string" && (EDITABLE_FILE_TASK_KIND_VALUES as readonly string[]).includes(value);
}

function isEditableFileTaskStatus(value: unknown): value is EditableFileTaskStatus {
  return typeof value === "string" && (EDITABLE_FILE_TASK_STATUS_VALUES as readonly string[]).includes(value);
}

function isEditableFileTaskResult(value: unknown): value is EditableFileTaskResult {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    (typeof (value as Record<string, unknown>).conversation_id === "undefined" || typeof (value as Record<string, unknown>).conversation_id === "string") &&
    (typeof (value as Record<string, unknown>).primary_path === "undefined" || typeof (value as Record<string, unknown>).primary_path === "string") &&
    (typeof (value as Record<string, unknown>).zip_path === "undefined" || typeof (value as Record<string, unknown>).zip_path === "string")
  );
}

function normalizeEditableFileHistoryItem(item: Partial<EditableFileHistoryItem>): EditableFileHistoryItem | null {
  const taskId = typeof item.taskId === "string" ? item.taskId.trim() : "";
  const kind = isEditableFileTaskKind(item.kind) ? item.kind : null;
  const prompt = typeof item.prompt === "string" ? item.prompt.trim() : "";
  const status = isEditableFileTaskStatus(item.status) ? item.status : null;
  const createdAt = typeof item.createdAt === "string" ? item.createdAt.trim() : "";
  const updatedAt = typeof item.updatedAt === "string" ? item.updatedAt.trim() : "";
  if (!taskId || !kind || !prompt || !status || !createdAt || !updatedAt) {
    return null;
  }

  const clientTaskId = typeof item.clientTaskId === "string" ? item.clientTaskId.trim() : "";
  const result = isEditableFileTaskResult(item.result) ? item.result : undefined;
  const error = typeof item.error === "string" && item.error.trim() ? item.error : undefined;

  return {
    taskId,
    kind,
    prompt,
    status,
    createdAt,
    updatedAt,
    ...(clientTaskId ? { clientTaskId } : {}),
    ...(typeof result !== "undefined" ? { result } : {}),
    ...(error ? { error } : {}),
  };
}

export function sortEditableFileHistoryItems(items: EditableFileHistoryItem[]): EditableFileHistoryItem[] {
  return [...items].sort((a, b) => getTimestamp(b.updatedAt) - getTimestamp(a.updatedAt));
}

export function mergeEditableFileHistoryItems(
  existing: EditableFileHistoryItem[],
  incoming: EditableFileHistoryItem[],
): EditableFileHistoryItem[] {
  const merged = new Map<string, EditableFileHistoryItem>();

  for (const item of [...existing, ...incoming]) {
    const normalized = normalizeEditableFileHistoryItem(item);
    if (!normalized) {
      continue;
    }
    const current = merged.get(normalized.taskId);
    if (!current || getTimestamp(normalized.updatedAt) >= getTimestamp(current.updatedAt)) {
      merged.set(normalized.taskId, normalized);
    }
  }

  return sortEditableFileHistoryItems([...merged.values()]);
}

async function getEditableFileHistoryStorageKey() {
  const session = await getStoredAuthSession();
  return buildEditableFileHistoryStorageKey(session);
}

async function readStoredEditableFileHistory(storageKey?: string): Promise<EditableFileHistoryItem[]> {
  const key = storageKey || (await getEditableFileHistoryStorageKey());
  const items = (await editableFileHistoryStorage.getItem<Array<Partial<EditableFileHistoryItem>>>(key)) || [];
  return sortEditableFileHistoryItems(
    items
      .map((item) => normalizeEditableFileHistoryItem(item))
      .filter((item): item is EditableFileHistoryItem => Boolean(item)),
  );
}

function queueEditableFileHistoryWrite<T>(operation: () => Promise<T>): Promise<T> {
  const result = editableFileHistoryWriteQueue.then(operation);
  editableFileHistoryWriteQueue = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

export async function listEditableFileHistory(): Promise<EditableFileHistoryItem[]> {
  return readStoredEditableFileHistory();
}

export async function upsertEditableFileHistoryItems(items: EditableFileHistoryItem[]): Promise<void> {
  await queueEditableFileHistoryWrite(async () => {
    const storageKey = await getEditableFileHistoryStorageKey();
    const existing = await readStoredEditableFileHistory(storageKey);
    const nextItems = mergeEditableFileHistoryItems(existing, items);
    await editableFileHistoryStorage.setItem(storageKey, nextItems);
  });
}

export async function clearEditableFileHistory(): Promise<void> {
  await queueEditableFileHistoryWrite(async () => {
    await editableFileHistoryStorage.removeItem(await getEditableFileHistoryStorageKey());
  });
}
