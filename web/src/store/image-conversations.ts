"use client";

import localforage from "localforage";

import {
  DEFAULT_CHAT_MODEL,
  DEFAULT_IMAGE_MODEL,
  isChatModel,
  isImageModel,
  isImageQuality,
  isImageTaskModel,
  type ImageModel,
  type ImageQuality,
} from "@/lib/api";
import { getStoredAuthSession, type StoredAuthSession } from "@/store/auth";

export type ImageConversationMode = "chat" | "generate" | "image" | "edit";
export type StoredReferenceImageSource = "upload" | "conversation";

export type StoredReferenceImage = {
  name: string;
  type: string;
  dataUrl: string;
  source?: StoredReferenceImageSource;
};

export type StoredImage = {
  id: string;
  taskId?: string;
  status?: "loading" | "success" | "error" | "cancelled" | "message";
  b64_json?: string;
  url?: string;
  revised_prompt?: string;
  error?: string;
  text_response?: string;
};

export type ImageTurnStatus = "queued" | "generating" | "success" | "error" | "cancelled" | "message";

export type ImageTurn = {
  id: string;
  prompt: string;
  model: ImageModel;
  mode: ImageConversationMode;
  referenceImages: StoredReferenceImage[];
  count: number;
  size: string;
  quality?: ImageQuality;
  images: StoredImage[];
  createdAt: string;
  status: ImageTurnStatus;
  error?: string;
};

export type ImageConversation = {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  turns: ImageTurn[];
};

export type ImageConversationStats = {
  queued: number;
  running: number;
};

const imageConversationStorage = localforage.createInstance({
  name: "chatgpt2api",
  storeName: "image_conversations",
});

const IMAGE_CONVERSATIONS_KEY_PREFIX = "items";
let imageConversationWriteQueue: Promise<void> = Promise.resolve();

function conversationScopeFromSession(session: StoredAuthSession | null) {
  if (!session) {
    return "anonymous";
  }
  const subjectId = session.subjectId.trim();
  if (!subjectId) {
    return `${session.provider || "local"}:${session.role}:unknown`;
  }
  return `${session.provider || "local"}:${session.role}:${subjectId}`;
}

async function imageConversationsStorageKey() {
  const session = await getStoredAuthSession();
  return `${IMAGE_CONVERSATIONS_KEY_PREFIX}:${conversationScopeFromSession(session)}`;
}

function normalizeStoredImage(image: StoredImage): StoredImage {
  const normalized = {
    ...image,
    taskId: typeof image.taskId === "string" && image.taskId ? image.taskId : undefined,
    url: typeof image.url === "string" && image.url ? image.url : undefined,
    revised_prompt: typeof image.revised_prompt === "string" ? image.revised_prompt : undefined,
    text_response: typeof image.text_response === "string" && image.text_response ? image.text_response : undefined,
  };
  if (image.status === "loading" || image.status === "error" || image.status === "success" || image.status === "cancelled" || image.status === "message") {
    return normalized;
  }
  return {
    ...normalized,
    status: image.b64_json || image.url ? "success" : "loading",
  };
}

function normalizeReferenceImage(image: StoredReferenceImage & Record<string, unknown>): StoredReferenceImage {
  const source =
    image.source === "upload" || image.source === "conversation"
      ? image.source
      : undefined;
  return {
    name: image.name || "reference.png",
    type: image.type || "image/png",
    dataUrl: image.dataUrl,
    ...(source ? { source } : {}),
  };
}

function normalizeImageMode(value: unknown, referenceImages: StoredReferenceImage[]): ImageConversationMode {
  if (value === "chat") {
    return "chat";
  }
  if (value === "generate") {
    return "generate";
  }
  if (value === "image") {
    return "image";
  }
  if (value === "edit") {
    return referenceImages.some((image) => image.source === "conversation") ? "edit" : "image";
  }
  return referenceImages.length > 0 ? "image" : "generate";
}

function dataUrlMimeType(dataUrl: string) {
  const match = dataUrl.match(/^data:(.*?);base64,/);
  return match?.[1] || "image/png";
}

function getLegacyReferenceImages(source: Record<string, unknown>): StoredReferenceImage[] {
  if (Array.isArray(source.referenceImages)) {
    return source.referenceImages
      .filter((image): image is StoredReferenceImage => {
        if (!image || typeof image !== "object") {
          return false;
        }
        const candidate = image as StoredReferenceImage;
        return typeof candidate.dataUrl === "string" && candidate.dataUrl.length > 0;
      })
      .map(normalizeReferenceImage);
  }

  if (source.sourceImage && typeof source.sourceImage === "object") {
    const image = source.sourceImage as { dataUrl?: unknown; fileName?: unknown };
    if (typeof image.dataUrl === "string" && image.dataUrl) {
      return [
        {
          name: typeof image.fileName === "string" && image.fileName ? image.fileName : "reference.png",
          type: dataUrlMimeType(image.dataUrl),
          dataUrl: image.dataUrl,
          source: "upload",
        },
      ];
    }
  }

  return [];
}

function normalizeTurn(turn: ImageTurn & Record<string, unknown>): ImageTurn {
  const normalizedImages = Array.isArray(turn.images) ? turn.images.map(normalizeStoredImage) : [];
  const referenceImages = getLegacyReferenceImages(turn);
  const mode = normalizeImageMode(turn.mode, referenceImages);
  const model =
    mode === "chat"
      ? isChatModel(turn.model)
        ? turn.model
        : DEFAULT_CHAT_MODEL
      : isImageTaskModel(turn.model)
        ? turn.model
        : DEFAULT_IMAGE_MODEL;
  const derivedStatus: ImageTurnStatus =
    normalizedImages.some((image) => image.status === "loading")
      ? "generating"
      : normalizedImages.some((image) => image.status === "error")
        ? "error"
        : normalizedImages.some((image) => image.status === "cancelled")
          ? "cancelled"
          : normalizedImages.some((image) => image.status === "message")
            ? "message"
            : "success";

  return {
    id: String(turn.id || `${Date.now()}`),
    prompt: String(turn.prompt || ""),
    model,
    mode,
    referenceImages,
    count: Math.max(1, Number(turn.count || normalizedImages.length || 1)),
    size: typeof turn.size === "string" ? turn.size : "",
    quality: isImageQuality(turn.quality) ? turn.quality : undefined,
    images: normalizedImages,
    createdAt: String(turn.createdAt || new Date().toISOString()),
    status:
      turn.status === "queued" ||
      turn.status === "generating" ||
      turn.status === "success" ||
      turn.status === "error" ||
      turn.status === "cancelled" ||
      turn.status === "message"
        ? turn.status
        : derivedStatus,
    error: typeof turn.error === "string" ? turn.error : undefined,
  };
}

function normalizeConversation(conversation: ImageConversation & Record<string, unknown>): ImageConversation {
  const legacyReferenceImages = getLegacyReferenceImages(conversation);
  const legacyMode = normalizeImageMode(conversation.mode, legacyReferenceImages);
  const turns = Array.isArray(conversation.turns)
    ? conversation.turns.map((turn) => normalizeTurn(turn as ImageTurn & Record<string, unknown>))
    : [
        normalizeTurn({
          id: String(conversation.id || `${Date.now()}`),
          prompt: String(conversation.prompt || ""),
          model: isImageModel(conversation.model)
            ? conversation.model
            : legacyMode === "chat"
              ? DEFAULT_CHAT_MODEL
              : DEFAULT_IMAGE_MODEL,
          mode: legacyMode,
          referenceImages: legacyReferenceImages,
          count: Number(conversation.count || 1),
          size: typeof conversation.size === "string" ? conversation.size : "",
          quality: isImageQuality(conversation.quality) ? conversation.quality : undefined,
          images: Array.isArray(conversation.images) ? (conversation.images as StoredImage[]) : [],
          createdAt: String(conversation.createdAt || new Date().toISOString()),
          status:
            conversation.status === "generating" || conversation.status === "success" || conversation.status === "error" || conversation.status === "message"
              ? conversation.status
              : "success",
          error: typeof conversation.error === "string" ? conversation.error : undefined,
        }),
      ];
  const lastTurn = turns.length > 0 ? turns[turns.length - 1] : null;

  return {
    id: String(conversation.id || `${Date.now()}`),
    title: String(conversation.title || ""),
    createdAt: String(conversation.createdAt || lastTurn?.createdAt || new Date().toISOString()),
    updatedAt: String(conversation.updatedAt || lastTurn?.createdAt || new Date().toISOString()),
    turns,
  };
}

function sortImageConversations(conversations: ImageConversation[]): ImageConversation[] {
  return [...conversations].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

function getTimestamp(value: string) {
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

function pickLatestConversation(current: ImageConversation, next: ImageConversation) {
  return getTimestamp(next.updatedAt) >= getTimestamp(current.updatedAt) ? next : current;
}

function queueImageConversationWrite<T>(operation: () => Promise<T>): Promise<T> {
  const result = imageConversationWriteQueue.then(operation);
  imageConversationWriteQueue = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

async function readStoredImageConversations(storageKey?: string): Promise<ImageConversation[]> {
  storageKey = storageKey || await imageConversationsStorageKey();
  const items =
    (await imageConversationStorage.getItem<Array<ImageConversation & Record<string, unknown>>>(
      storageKey,
    )) || [];
  return items.map(normalizeConversation);
}

export async function listImageConversations(): Promise<ImageConversation[]> {
  return sortImageConversations(await readStoredImageConversations());
}

export async function saveImageConversations(conversations: ImageConversation[]): Promise<void> {
  await queueImageConversationWrite(async () => {
    const storageKey = await imageConversationsStorageKey();
    const items = await readStoredImageConversations(storageKey);
    const conversationMap = new Map(items.map((item) => [item.id, item]));
    for (const conversation of conversations.map(normalizeConversation)) {
      const current = conversationMap.get(conversation.id);
      conversationMap.set(conversation.id, current ? pickLatestConversation(current, conversation) : conversation);
    }
    await imageConversationStorage.setItem(
      storageKey,
      sortImageConversations([...conversationMap.values()]),
    );
  });
}

export async function saveImageConversation(conversation: ImageConversation): Promise<void> {
  await queueImageConversationWrite(async () => {
    const storageKey = await imageConversationsStorageKey();
    const items = await readStoredImageConversations(storageKey);
    const nextConversation = normalizeConversation(conversation);
    const current = items.find((item) => item.id === nextConversation.id);
    const persistedConversation = current ? pickLatestConversation(current, nextConversation) : nextConversation;
    const nextItems = sortImageConversations([
      persistedConversation,
      ...items.filter((item) => item.id !== persistedConversation.id),
    ]);
    await imageConversationStorage.setItem(storageKey, nextItems);
  });
}

export async function deleteImageConversation(id: string): Promise<void> {
  await queueImageConversationWrite(async () => {
    const storageKey = await imageConversationsStorageKey();
    const items = await readStoredImageConversations(storageKey);
    await imageConversationStorage.setItem(
      storageKey,
      items.filter((item) => item.id !== id),
    );
  });
}

export async function clearImageConversations(): Promise<void> {
  await queueImageConversationWrite(async () => {
    await imageConversationStorage.removeItem(await imageConversationsStorageKey());
  });
}

export function getImageConversationStats(conversation: ImageConversation | null): ImageConversationStats {
  if (!conversation) {
    return { queued: 0, running: 0 };
  }

  return conversation.turns.reduce(
    (acc, turn) => {
      if (turn.status === "queued") {
        acc.queued += 1;
      } else if (turn.status === "generating") {
        acc.running += 1;
      }
      return acc;
    },
    { queued: 0, running: 0 },
  );
}
