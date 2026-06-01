import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StoredAuthSession } from "@/store/auth";
import type { EditableFileHistoryItem } from "./editable-file-history";

const mockStorage = vi.hoisted(() => new Map<string, unknown>());
const mockSessionState = vi.hoisted(() => ({
  session: null as StoredAuthSession | null,
}));

vi.mock("localforage", () => ({
  default: {
    createInstance: () => ({
      getItem: async (key: string) => mockStorage.get(key) ?? null,
      setItem: async (key: string, value: unknown) => {
        mockStorage.set(key, value);
        return value;
      },
      removeItem: async (key: string) => {
        mockStorage.delete(key);
      },
    }),
  },
}));

vi.mock("@/store/auth", async () => {
  const actual = await vi.importActual<typeof import("@/store/auth")>("@/store/auth");
  return {
    ...actual,
    getStoredAuthSession: vi.fn(async () => mockSessionState.session),
  };
});

import {
  buildEditableFileHistoryStorageKey,
  clearEditableFileHistory,
  listEditableFileHistory,
  mergeEditableFileHistoryItems,
  upsertEditableFileHistoryItems,
} from "./editable-file-history";

describe("editable file history store", () => {
  beforeEach(() => {
    mockStorage.clear();
    mockSessionState.session = {
      key: "session-key",
      role: "user",
      subjectId: "subject-a",
      name: "Alice",
      provider: "local",
      creationConcurrentLimit: 1,
      creationRpmLimit: 1,
      billing: null,
      menuPaths: [],
      apiPermissions: [],
      menus: [],
    };
  });

  it("isolates storage keys by session key and subjectId", () => {
    const baseSession = mockSessionState.session as StoredAuthSession;
    expect(
      buildEditableFileHistoryStorageKey({ ...baseSession, key: "session-a", subjectId: "subject-a" }),
    ).toBe("items:session-a:local:user:subject-a");
    expect(
      buildEditableFileHistoryStorageKey({ ...baseSession, key: "session-b", subjectId: "subject-a" }),
    ).not.toBe(buildEditableFileHistoryStorageKey({ ...baseSession, key: "session-a", subjectId: "subject-a" }));
  });

  it("keeps the newer item for the same taskId", () => {
    const older = {
      taskId: "task-1",
      kind: "ppt",
      prompt: "old",
      status: "running",
      createdAt: "2026-01-01T00:00:00.000Z",
      updatedAt: "2026-01-01T00:01:00.000Z",
    } satisfies EditableFileHistoryItem;
    const newer = {
      taskId: "task-1",
      kind: "ppt",
      prompt: "new",
      status: "success",
      createdAt: "2026-01-01T00:00:00.000Z",
      updatedAt: "2026-01-01T00:02:00.000Z",
      result: { primary_path: "2026/01/01/deck.pptx" },
    } satisfies EditableFileHistoryItem;

    expect(mergeEditableFileHistoryItems([older], [newer])).toEqual([newer]);
  });

  it("lists, upserts, and clears the current session history", async () => {
    await clearEditableFileHistory();
    expect(await listEditableFileHistory()).toEqual([]);

    await upsertEditableFileHistoryItems([
      {
        taskId: "task-1",
        kind: "ppt",
        prompt: "first",
        status: "running",
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:01:00.000Z",
      },
      {
        taskId: "task-2",
        kind: "psd",
        prompt: "second",
        status: "success",
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:03:00.000Z",
      },
    ]);

    await upsertEditableFileHistoryItems([
      {
        taskId: "task-1",
        kind: "ppt",
        prompt: "first updated",
        status: "success",
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:04:00.000Z",
        clientTaskId: "client-task-1",
      },
    ]);

    expect(await listEditableFileHistory()).toEqual([
      {
        taskId: "task-1",
        kind: "ppt",
        prompt: "first updated",
        status: "success",
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:04:00.000Z",
        clientTaskId: "client-task-1",
      },
      {
        taskId: "task-2",
        kind: "psd",
        prompt: "second",
        status: "success",
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:03:00.000Z",
      },
    ]);

    await clearEditableFileHistory();
    expect(await listEditableFileHistory()).toEqual([]);
  });
});
