"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { History, ImagePlus, LoaderCircle, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { ImageComposer } from "@/app/image/components/image-composer";
import { ImagePromptMarket } from "@/app/image/components/image-prompt-market";
import { ImageResults, type ImageLightboxItem, type ImageTurnProgress } from "@/app/image/components/image-results";
import type { BananaPrompt } from "@/app/image/banana-prompts";
import { IMAGE_QUALITY_OPTIONS, IMAGE_SIZE_OPTIONS } from "@/app/image/image-options";
import { IMAGE_PROMPT_PRESETS, type ImagePromptPreset } from "@/app/image/image-presets";
import { ImageSidebar } from "@/app/image/components/image-sidebar";
import { AnnouncementNotifications } from "@/components/announcement-banner";
import { ImageLightbox } from "@/components/image-lightbox";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  cancelImageTask,
  CHAT_MODEL_OPTIONS,
  createChatCompletion,
  createImageEditTask,
  createImageGenerationTask,
  DEFAULT_CHAT_MODEL,
  DEFAULT_IMAGE_MODEL,
  fetchImageTasks,
  IMAGE_TASK_MODEL_OPTIONS,
  isChatModel,
  isImageModel,
  isImageQuality,
  isImageTaskModel,
  type ImageModel,
  type ImageQuality,
  type ImageTask,
  type ImageTaskMessage,
} from "@/lib/api";
import { cn } from "@/lib/utils";
import { useAuthGuard } from "@/lib/use-auth-guard";
import {
  clearImageConversations,
  deleteImageConversation,
  getImageConversationStats,
  listImageConversations,
  saveImageConversation,
  saveImageConversations,
  type ImageConversation,
  type ImageConversationMode,
  type ImageTurn,
  type ImageTurnStatus,
  type StoredImage,
  type StoredReferenceImage,
} from "@/store/image-conversations";

const ACTIVE_CONVERSATION_STORAGE_KEY = "chatgpt2api:image_active_conversation_id";
const COMPOSER_MODE_STORAGE_KEY = "chatgpt2api:image_composer_mode";
const IMAGE_MODEL_STORAGE_KEY = "chatgpt2api:image_last_model";
const IMAGE_SIZE_STORAGE_KEY = "chatgpt2api:image_last_size";
const IMAGE_QUALITY_STORAGE_KEY = "chatgpt2api:image_last_quality";
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";
const DEFAULT_IMAGE_QUALITY: ImageQuality = "high";
const activeConversationQueueIds = new Set<string>();
const EMPTY_IMAGE_SIZE_SELECT_VALUE = "__empty__";

type ComposerMode = "chat" | "image";

type EditingTurnDraft = {
  conversationId: string;
  turnId: string;
  prompt: string;
  model: ImageModel;
  mode: ImageConversationMode;
  count: string;
  size: string;
  quality: ImageQuality;
  referenceImages: StoredReferenceImage[];
};

function buildConversationTitle(prompt: string) {
  const trimmed = prompt.trim();
  if (trimmed.length <= 12) {
    return trimmed;
  }
  return `${trimmed.slice(0, 12)}...`;
}

function formatConversationTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function createId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(new Error("读取参考图失败"));
    reader.readAsDataURL(file);
  });
}

function dataUrlToFile(dataUrl: string, fileName: string, mimeType?: string) {
  const [header, content] = dataUrl.split(",", 2);
  const matchedMimeType = header.match(/data:(.*?);base64/)?.[1];
  const binary = atob(content || "");
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new File([bytes], fileName, { type: mimeType || matchedMimeType || "image/png" });
}

function buildReferenceImageFromResult(image: StoredImage, fileName: string): StoredReferenceImage | null {
  if (!image.b64_json) {
    return null;
  }

  return {
    name: fileName,
    type: "image/png",
    dataUrl: `data:image/png;base64,${image.b64_json}`,
  };
}

async function fetchImageAsFile(url: string, fileName: string) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error("读取结果图失败");
  }
  const blob = await response.blob();
  return new File([blob], fileName, { type: blob.type || "image/png" });
}

function buildMarketReferenceFileName(url: string, index: number) {
  try {
    const name = decodeURIComponent(new URL(url).pathname.split("/").filter(Boolean).pop() || "");
    if (name) {
      return name.includes(".") ? name : `${name}.png`;
    }
  } catch {
    // Keep a deterministic fallback for malformed source links.
  }
  return `banana-reference-${index + 1}.png`;
}

async function buildMarketReferenceImage(url: string, index: number): Promise<StoredReferenceImage> {
  const file = await fetchImageAsFile(url, buildMarketReferenceFileName(url, index));
  return {
    name: file.name,
    type: file.type || "image/png",
    dataUrl: await readFileAsDataUrl(file),
    source: "upload",
  };
}

async function buildReferenceImageFromStoredImage(image: StoredImage, fileName: string) {
  const direct = buildReferenceImageFromResult(image, fileName);
  if (direct) {
    return {
      referenceImage: direct,
      file: dataUrlToFile(direct.dataUrl, direct.name, direct.type),
    };
  }

  if (!image.url) {
    return null;
  }
  const file = await fetchImageAsFile(image.url, fileName);
  return {
    referenceImage: {
      name: file.name,
      type: file.type || "image/png",
      dataUrl: await readFileAsDataUrl(file),
    },
    file,
  };
}

const IMAGE_TASK_IMAGE_COUNT = 1;

function normalizeRequestedImageCount(value: string | number) {
  return Math.max(1, Math.min(10, Number(value) || 1));
}

function imageTaskBatchId(turnId: string, imageIndex: number) {
  return `${turnId}-task-${Math.floor(imageIndex / IMAGE_TASK_IMAGE_COUNT)}`;
}

function imageTurnProgressKey(conversationId: string, turnId: string) {
  return `${conversationId}:${turnId}`;
}

function imageTaskIdForImage(turnId: string, images: StoredImage[], imageIndex: number) {
  return images[imageIndex]?.taskId || imageTaskBatchId(turnId, imageIndex);
}

function imageDataIndexForTask(images: StoredImage[], imageIndex: number) {
  const taskId = images[imageIndex]?.taskId || images[imageIndex]?.id;
  if (!taskId) {
    return 0;
  }
  return images.slice(0, imageIndex + 1).filter((image) => (image.taskId || image.id) === taskId).length - 1;
}

const STORED_IMAGE_FIELDS: Array<keyof StoredImage> = [
  "id",
  "taskId",
  "status",
  "b64_json",
  "url",
  "revised_prompt",
  "error",
  "text_response",
];

function updateStoredImage(image: StoredImage, updates: Partial<StoredImage>): StoredImage {
  const next = { ...image, ...updates };
  return STORED_IMAGE_FIELDS.every((field) => image[field] === next[field]) ? image : next;
}

function taskDataToStoredImage(image: StoredImage, task: ImageTask, dataIndex = 0): StoredImage {
  if (task.status === "success") {
    const item = task.data?.[dataIndex];
    if (!item?.b64_json && !item?.url) {
      if (dataIndex > 0 && image.taskId !== image.id) {
        return updateStoredImage(image, {
          taskId: image.id,
          status: "loading",
          error: undefined,
        });
      }
      return updateStoredImage(image, {
        taskId: task.id,
        status: "error",
        error: `未返回第 ${dataIndex + 1} 张图片数据`,
      });
    }
    return updateStoredImage(image, {
      taskId: task.id,
      status: "success",
      b64_json: item.b64_json,
      url: item.url,
      revised_prompt: item.revised_prompt,
      text_response: undefined,
      error: undefined,
    });
  }

  if (task.status === "error") {
    if (task.output_type === "text") {
      return updateStoredImage(image, {
        taskId: task.id,
        status: "message",
        text_response: task.error || "",
        b64_json: undefined,
        url: undefined,
        revised_prompt: undefined,
        error: undefined,
      });
    }
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, {
        taskId: task.id,
        status: "success",
        b64_json: item.b64_json,
        url: item.url,
        revised_prompt: item.revised_prompt,
        text_response: undefined,
        error: undefined,
      });
    }
    return updateStoredImage(image, {
      taskId: task.id,
      status: "error",
      text_response: undefined,
      error: formatImageTaskErrorMessage(task.error || "生成失败"),
    });
  }

  if (task.status === "cancelled") {
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, {
        taskId: task.id,
        status: "success",
        b64_json: item.b64_json,
        url: item.url,
        revised_prompt: item.revised_prompt,
        text_response: undefined,
        error: undefined,
      });
    }
    return updateStoredImage(image, {
      taskId: task.id,
      status: "cancelled",
      error: task.error || "任务已终止",
    });
  }

  return updateStoredImage(image, {
    taskId: task.id,
    status: "loading",
    text_response: undefined,
    error: undefined,
  });
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function pickFallbackConversationId(conversations: ImageConversation[]) {
  const activeConversation = conversations.find((conversation) =>
    conversation.turns.some((turn) => turn.status === "queued" || turn.status === "generating"),
  );
  return activeConversation?.id ?? conversations[0]?.id ?? null;
}

function sortImageConversations(conversations: ImageConversation[]) {
  return [...conversations].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

function getStoredImageModel(): ImageModel {
  if (typeof window === "undefined") {
    return DEFAULT_IMAGE_MODEL;
  }
  const storedModel = window.localStorage.getItem(IMAGE_MODEL_STORAGE_KEY);
  return isImageModel(storedModel) ? storedModel : DEFAULT_IMAGE_MODEL;
}

function getStoredComposerMode(): ComposerMode {
  if (typeof window === "undefined") {
    return "image";
  }
  return window.localStorage.getItem(COMPOSER_MODE_STORAGE_KEY) === "chat" ? "chat" : "image";
}

function getStoredImageQuality(): ImageQuality {
  if (typeof window === "undefined") {
    return DEFAULT_IMAGE_QUALITY;
  }
  const storedQuality = window.localStorage.getItem(IMAGE_QUALITY_STORAGE_KEY);
  return isImageQuality(storedQuality) ? storedQuality : DEFAULT_IMAGE_QUALITY;
}

function buildTurnOutcomeMessage(successCount: number, failedCount: number, cancelledCount: number) {
  const parts = [`成功 ${successCount} 张`];
  if (failedCount > 0) {
    parts.push(`失败 ${failedCount} 张`);
  }
  if (cancelledCount > 0) {
    parts.push(`终止 ${cancelledCount} 张`);
  }
  return parts.join("，");
}

function formatImageTaskErrorMessage(message: string) {
  const trimmed = String(message || "").trim();
  if (!trimmed) {
    return "生成图片失败";
  }

  const normalized = trimmed.toLowerCase();
  if (normalized.includes("an error occurred while processing your request")) {
    const requestId = trimmed.match(/request id\s+([a-z0-9-]+)/i)?.[1];
    return [
      "提示词内容过多，或当前分辨率/质量组合过高。",
      "建议减少提示词内容，或降低分辨率、质量后重试。",
      requestId ? `请求 ID：${requestId}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  }
  if (normalized.includes("no images generated") && normalized.includes("model may have refused")) {
    return "没有生成图片，模型可能检测到敏感内容并拒绝了这次请求，请调整提示词后重试。";
  }
  if (normalized.includes("timed out waiting for async image generation")) {
    return "图片生成等待超时，建议稍后重试，或降低分辨率、质量后再试。";
  }
  if (normalized.includes("no available image quota")) {
    return "当前没有可用的图片额度，请检查账号额度或稍后重试。";
  }

  return trimmed;
}

function formatImageTaskError(error: unknown, fallback = "生成图片失败") {
  return formatImageTaskErrorMessage(error instanceof Error ? error.message : String(error || fallback));
}

function deriveTurnStatus(turn: ImageTurn): Pick<ImageTurn, "status" | "error"> {
  const loadingCount = turn.images.filter((image) => image.status === "loading").length;
  const failedCount = turn.images.filter((image) => image.status === "error").length;
  const successCount = turn.images.filter((image) => image.status === "success").length;
  const cancelledCount = turn.images.filter((image) => image.status === "cancelled").length;
  const messageCount = turn.images.filter((image) => image.status === "message").length;
  if (loadingCount > 0) {
    return { status: turn.status === "queued" ? "queued" : "generating", error: undefined };
  }
  if (failedCount > 0) {
    return { status: "error", error: buildTurnOutcomeMessage(successCount, failedCount, cancelledCount) };
  }
  if (cancelledCount > 0) {
    return { status: "cancelled", error: buildTurnOutcomeMessage(successCount, failedCount, cancelledCount) };
  }
  if (successCount > 0) {
    return { status: "success", error: undefined };
  }
  if (messageCount > 0) {
    return { status: "message", error: undefined };
  }
  return { status: "queued", error: undefined };
}

function isTurnInProgress(turn: ImageTurn) {
  return (
    turn.status === "queued" ||
    turn.status === "generating" ||
    turn.images.some((image) => image.status === "loading")
  );
}

function usesReferenceImages(mode: ImageConversationMode) {
  return mode === "image" || mode === "edit";
}

function isMissingBatchImageDataError(error?: string) {
  return typeof error === "string" && error.startsWith("未返回第 ") && error.endsWith(" 张图片数据");
}

function getComposerConversationMode(composerMode: ComposerMode, referenceImages: StoredReferenceImage[]): ImageConversationMode {
  if (composerMode === "chat") {
    return "chat";
  }
  if (referenceImages.length === 0) {
    return "generate";
  }
  return referenceImages.some((image) => image.source === "conversation") ? "edit" : "image";
}

function chatCompletionContentToText(content: unknown): string {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((item) => {
      if (!item || typeof item !== "object") {
        return "";
      }
      const block = item as { text?: unknown };
      return typeof block.text === "string" ? block.text : "";
    })
    .filter(Boolean)
    .join("\n");
}

function chatCompletionResponseText(response: Awaited<ReturnType<typeof createChatCompletion>>) {
  return chatCompletionContentToText(response.choices?.[0]?.message?.content).trim();
}

function buildImageTaskMessages(conversation: ImageConversation, activeTurnId: string): ImageTaskMessage[] {
  const messages: ImageTaskMessage[] = [];
  for (const turn of conversation.turns) {
    const prompt = turn.prompt.trim();
    if (prompt) {
      messages.push({ role: "user", content: prompt });
    }
    if (turn.id === activeTurnId) {
      break;
    }

    const assistantParts = turn.images.flatMap((image) => {
      if (image.status === "message" && image.text_response?.trim()) {
        return [image.text_response.trim()];
      }
      if (image.status === "success" && image.revised_prompt?.trim()) {
        return [`Generated image: ${image.revised_prompt.trim()}`];
      }
      return [];
    });
    if (assistantParts.length > 0) {
      messages.push({ role: "assistant", content: assistantParts.join("\n\n") });
    }
  }
  return messages;
}

async function syncConversationImageTasks(items: ImageConversation[]) {
  const taskIds = Array.from(
    new Set(
      items.flatMap((conversation) =>
        conversation.turns.flatMap((turn) =>
          turn.images.flatMap((image) => (image.status === "loading" && image.taskId ? [image.taskId] : [])),
        ),
      ),
    ),
  );
  if (taskIds.length === 0) {
    return items;
  }

  let taskList: Awaited<ReturnType<typeof fetchImageTasks>>;
  try {
    taskList = await fetchImageTasks(taskIds);
  } catch {
    return items;
  }
  const taskMap = new Map(taskList.items.map((task) => [task.id, task]));
  let changed = false;
  const normalized = items.map((conversation) => {
    let completedActiveTurn = false;
    const turns = conversation.turns.map((turn) => {
      let turnChanged = false;
      const images = turn.images.map((image, imageIndex) => {
        if (image.status !== "loading" || !image.taskId) {
          return image;
        }
        const task = taskMap.get(image.taskId);
        if (!task) {
          return image;
        }
        const nextImage = taskDataToStoredImage(image, task, imageDataIndexForTask(turn.images, imageIndex));
        if (nextImage !== image) {
          turnChanged = true;
        }
        return nextImage;
      });
      if (!turnChanged) {
        return turn;
      }
      changed = true;
      const derived = deriveTurnStatus({ ...turn, images });
      const nextTurn = {
        ...turn,
        ...derived,
        images,
      };
      if (isTurnInProgress(turn) && !isTurnInProgress(nextTurn)) {
        completedActiveTurn = true;
      }
      return nextTurn;
    });
    if (turns === conversation.turns || !turns.some((turn, index) => turn !== conversation.turns[index])) {
      return conversation;
    }
    const nextConversation = {
      ...conversation,
      turns,
    };
    return completedActiveTurn
      ? {
          ...nextConversation,
          updatedAt: new Date().toISOString(),
        }
      : nextConversation;
  });

  if (changed) {
    await saveImageConversations(normalized);
  }
  return normalized;
}

async function recoverConversationHistory(items: ImageConversation[]) {
  let changed = false;
  const normalized = items.map((conversation) => {
    const turns = conversation.turns.map((turn) => {
      let turnChanged = false;
      const recoveredImages = turn.images.map((image) => {
        if (image.status === "error" && isMissingBatchImageDataError(image.error)) {
          turnChanged = true;
          return {
            ...image,
            taskId: image.id,
            status: "loading" as const,
            error: undefined,
          };
        }
        return image;
      });

      if (turn.status !== "queued" && turn.status !== "generating") {
        if (!turnChanged) {
          return turn;
        }
        changed = true;
        const derived = deriveTurnStatus({ ...turn, status: "queued", images: recoveredImages });
        return {
          ...turn,
          ...derived,
          images: recoveredImages,
        };
      }

      const images = recoveredImages.map((image) => {
        if (image.status !== "loading" || image.taskId) {
          return image;
        }
        turnChanged = true;
        return {
          ...image,
          status: "error" as const,
          error: "页面刷新或任务中断，未找到可恢复的任务 ID",
        };
      });
      const derived = deriveTurnStatus({ ...turn, images });
      if (!turnChanged && derived.status === turn.status && derived.error === turn.error) {
        return turn;
      }
      changed = true;
      return {
        ...turn,
        ...derived,
        images,
      };
    });

    if (!turns.some((turn, index) => turn !== conversation.turns[index])) {
      return conversation;
    }

    return {
      ...conversation,
      turns,
      updatedAt: new Date().toISOString(),
    };
  });

  if (changed) {
    await saveImageConversations(normalized);
  }

  return syncConversationImageTasks(normalized);
}


function ImagePageContent() {
  const isSubmitDispatchingRef = useRef(false);
  const retryingImageIdsRef = useRef(new Set<string>());
  const cancelledTurnIdsRef = useRef(new Set<string>());
  const conversationsRef = useRef<ImageConversation[]>([]);
  const resultsViewportRef = useRef<HTMLDivElement>(null);
  const composerDockRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const editFileInputRef = useRef<HTMLInputElement>(null);

  const [imagePrompt, setImagePrompt] = useState("");
  const [composerMode, setComposerMode] = useState<ComposerMode>(getStoredComposerMode);
  const [imageModel, setImageModel] = useState<ImageModel>(getStoredImageModel);
  const [imageCount, setImageCount] = useState("1");
  const [imageSize, setImageSize] = useState("");
  const [imageQuality, setImageQuality] = useState<ImageQuality>(getStoredImageQuality);
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [isPromptMarketOpen, setIsPromptMarketOpen] = useState(false);
  const [referenceImages, setReferenceImages] = useState<StoredReferenceImage[]>([]);
  const [conversations, setConversations] = useState<ImageConversation[]>([]);
  const [selectedConversationId, setSelectedConversationId] = useState<string | null>(null);
  const [isLoadingHistory, setIsLoadingHistory] = useState(true);
  const [lightboxImages, setLightboxImages] = useState<ImageLightboxItem[]>([]);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [deleteConfirm, setDeleteConfirm] = useState<{ type: "one"; id: string } | { type: "all" } | null>(null);
  const [editingTurnDraft, setEditingTurnDraft] = useState<EditingTurnDraft | null>(null);
  const [progressByTurnKey, setProgressByTurnKey] = useState<Record<string, ImageTurnProgress>>({});
  const [progressNow, setProgressNow] = useState(Date.now());
  const [composerDockHeight, setComposerDockHeight] = useState(0);

  const parsedCount = useMemo(() => normalizeRequestedImageCount(imageCount), [imageCount]);
  const composerModelOptions = composerMode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_TASK_MODEL_OPTIONS;
  const selectedConversation = useMemo(
    () => conversations.find((item) => item.id === selectedConversationId) ?? null,
    [conversations, selectedConversationId],
  );
  const activeTaskCount = useMemo(
    () =>
      conversations.reduce((sum, conversation) => {
        const stats = getImageConversationStats(conversation);
        return sum + stats.queued + stats.running;
      }, 0),
    [conversations],
  );
  const deleteConfirmTitle = deleteConfirm?.type === "all" ? "清空历史记录" : deleteConfirm?.type === "one" ? "删除对话" : "";
  const deleteConfirmDescription =
    deleteConfirm?.type === "all"
      ? "确认删除全部图片历史记录吗？删除后无法恢复。"
      : deleteConfirm?.type === "one"
        ? "确认删除这条图片对话吗？删除后无法恢复。"
        : "";
  const imageOutputHint = useMemo(
    () => (
      <>
        <div>
          <span className="font-semibold text-stone-800">质量说明：</span>
          Low / Medium / High 会随请求下发，并补充到生成提示中；实际生效仍取决于当前账号和上游链路能力。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">分辨率限制：</span>
          Free 账号建议按约 1.57M 像素总量控制；Paid 账号的图片最长边最高支持 3840。需要 2K / 4K 时，请在提示词中明确目标尺寸。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">账号要求：</span>
          2K 及以上像素档建议使用 Plus / Pro / Team 等 Paid 账号。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">Auto 模式补充：</span>
          Auto 不会强制指定比例和分辨率，请直接在提示词里写明横竖版、画幅比例和目标输出尺寸。
        </div>
      </>
    ),
    [],
  );

  useEffect(() => {
    conversationsRef.current = conversations;
  }, [conversations]);

  useEffect(() => {
    const node = composerDockRef.current;
    if (!node) {
      return;
    }

    const updateComposerHeight = () => {
      const nextHeight = Math.ceil(node.getBoundingClientRect().height);
      setComposerDockHeight((currentHeight) => (currentHeight === nextHeight ? currentHeight : nextHeight));
    };

    updateComposerHeight();
    const observer = new ResizeObserver(updateComposerHeight);
    observer.observe(node);
    return () => {
      observer.disconnect();
    };
  }, []);

  useEffect(() => {
    if (activeTaskCount === 0 && Object.keys(progressByTurnKey).length === 0) {
      return;
    }

    setProgressNow(Date.now());
    const timer = window.setInterval(() => {
      setProgressNow(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(timer);
    };
  }, [activeTaskCount, progressByTurnKey]);

  useEffect(() => {
    let cancelled = false;

    const loadHistory = async () => {
      try {
        const storedSize = typeof window !== "undefined" ? window.localStorage.getItem(IMAGE_SIZE_STORAGE_KEY) : null;
        setImageSize(storedSize || "");

        const items = await listImageConversations();
        const normalizedItems = await recoverConversationHistory(items);
        if (cancelled) {
          return;
        }

        conversationsRef.current = normalizedItems;
        setConversations(normalizedItems);
        const storedConversationId =
          typeof window !== "undefined" ? window.localStorage.getItem(ACTIVE_CONVERSATION_STORAGE_KEY) : null;
        const nextSelectedConversationId =
          (storedConversationId && normalizedItems.some((conversation) => conversation.id === storedConversationId)
            ? storedConversationId
            : null) ?? pickFallbackConversationId(normalizedItems);
        setSelectedConversationId(nextSelectedConversationId);
      } catch (error) {
        const message = error instanceof Error ? error.message : "读取会话记录失败";
        toast.error(message);
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
    if (!selectedConversationId) {
      return;
    }

    resultsViewportRef.current?.scrollTo({
      top: resultsViewportRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [selectedConversationId, selectedConversation?.turns.length]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    if (selectedConversationId) {
      window.localStorage.setItem(ACTIVE_CONVERSATION_STORAGE_KEY, selectedConversationId);
    } else {
      window.localStorage.removeItem(ACTIVE_CONVERSATION_STORAGE_KEY);
    }
  }, [selectedConversationId]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    window.localStorage.setItem(COMPOSER_MODE_STORAGE_KEY, composerMode);
  }, [composerMode]);

  useEffect(() => {
    if (composerMode === "chat") {
      if (!isChatModel(imageModel)) {
        setImageModel(DEFAULT_CHAT_MODEL);
      }
      if (referenceImages.length > 0) {
        setReferenceImages([]);
        if (fileInputRef.current) {
          fileInputRef.current.value = "";
        }
      }
      return;
    }

    if (!isImageTaskModel(imageModel)) {
      setImageModel(DEFAULT_IMAGE_MODEL);
    }
  }, [composerMode, imageModel, referenceImages.length]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    window.localStorage.setItem(IMAGE_MODEL_STORAGE_KEY, imageModel);
  }, [imageModel]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    if (imageSize) {
      window.localStorage.setItem(IMAGE_SIZE_STORAGE_KEY, imageSize);
      return;
    }
    window.localStorage.removeItem(IMAGE_SIZE_STORAGE_KEY);
  }, [imageSize]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    window.localStorage.setItem(IMAGE_QUALITY_STORAGE_KEY, imageQuality);
  }, [imageQuality]);

  useEffect(() => {
    if (selectedConversationId && !conversations.some((conversation) => conversation.id === selectedConversationId)) {
      setSelectedConversationId(pickFallbackConversationId(conversations));
    }
  }, [conversations, selectedConversationId]);

  const persistConversation = async (conversation: ImageConversation) => {
    const nextConversations = sortImageConversations([
      conversation,
      ...conversationsRef.current.filter((item) => item.id !== conversation.id),
    ]);
    conversationsRef.current = nextConversations;
    setConversations(nextConversations);
    await saveImageConversation(conversation);
  };

  const updateConversation = useCallback(
    async (
      conversationId: string,
      updater: (current: ImageConversation | null) => ImageConversation,
      options: { persist?: boolean } = {},
    ) => {
      const current = conversationsRef.current.find((item) => item.id === conversationId) ?? null;
      const nextConversation = updater(current);
      const nextConversations = sortImageConversations([
        nextConversation,
        ...conversationsRef.current.filter((item) => item.id !== conversationId),
      ]);
      conversationsRef.current = nextConversations;
      setConversations(nextConversations);
      if (options.persist !== false) {
        await saveImageConversation(nextConversation);
      }
    },
    [],
  );

  const updateTurnProgress = useCallback(
    (conversationId: string, turnId: string, updates: Omit<ImageTurnProgress, "startedAt"> & { startedAt?: number }) => {
      const key = imageTurnProgressKey(conversationId, turnId);
      setProgressByTurnKey((current) => ({
        ...current,
        [key]: {
          ...updates,
          startedAt: updates.startedAt ?? current[key]?.startedAt ?? Date.now(),
        },
      }));
    },
    [],
  );

  const clearTurnProgress = useCallback((conversationId: string, turnId: string) => {
    const key = imageTurnProgressKey(conversationId, turnId);
    setProgressByTurnKey((current) => {
      if (!current[key]) {
        return current;
      }
      const next = { ...current };
      delete next[key];
      return next;
    });
  }, []);

  const clearComposerInputs = useCallback(() => {
    setImagePrompt("");
    setImageCount("1");
    setReferenceImages([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, []);

  const resetComposer = useCallback(() => {
    clearComposerInputs();
  }, [clearComposerInputs]);

  const handleComposerModeChange = useCallback((mode: ComposerMode) => {
    setComposerMode(mode);
    if (mode === "chat") {
      setReferenceImages([]);
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  }, []);

  const handleCreateDraft = () => {
    setSelectedConversationId(null);
    resetComposer();
    textareaRef.current?.focus();
  };

  const handleApplyPromptPreset = useCallback((preset: ImagePromptPreset) => {
    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(preset.prompt);
    setImageCount(String(preset.count));
    setImageSize(preset.size);
    setReferenceImages([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();
  }, []);

  const handleApplyMarketPrompt = useCallback(async (prompt: BananaPrompt) => {
    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(prompt.prompt);
    setImageCount("1");
    setImageSize("");
    setReferenceImages([]);
    setIsPromptMarketOpen(false);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();

    if (prompt.referenceImageUrls.length === 0) {
      toast.success("已套用提示词");
      return;
    }

    const toastId = toast.loading(`正在读取 ${prompt.referenceImageUrls.length} 张参考图`);
    const results = await Promise.allSettled(
      prompt.referenceImageUrls.map((url, index) => buildMarketReferenceImage(url, index)),
    );
    const loadedReferences = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));

    toast.dismiss(toastId);
    if (loadedReferences.length > 0) {
      setReferenceImages(loadedReferences);
    }
    if (loadedReferences.length === prompt.referenceImageUrls.length) {
      toast.success("已套用提示词和参考图");
    } else if (loadedReferences.length > 0) {
      toast.error(`已套用提示词，${prompt.referenceImageUrls.length - loadedReferences.length} 张参考图读取失败`);
    } else {
      toast.error("已套用提示词，但参考图读取失败");
    }
  }, []);

  const handleDeleteConversation = async (id: string) => {
    const nextConversations = conversations.filter((item) => item.id !== id);
    conversationsRef.current = nextConversations;
    setConversations(nextConversations);
    if (selectedConversationId === id) {
      setSelectedConversationId(pickFallbackConversationId(nextConversations));
      resetComposer();
    }

    try {
      await deleteImageConversation(id);
    } catch (error) {
      const message = error instanceof Error ? error.message : "删除会话失败";
      toast.error(message);
      const items = await listImageConversations();
      conversationsRef.current = items;
      setConversations(items);
    }
  };

  const handleClearHistory = async () => {
    try {
      await clearImageConversations();
      conversationsRef.current = [];
      setConversations([]);
      setSelectedConversationId(null);
      resetComposer();
      toast.success("已清空历史记录");
    } catch (error) {
      const message = error instanceof Error ? error.message : "清空历史记录失败";
      toast.error(message);
    }
  };

  const openDeleteConversationConfirm = (id: string) => {
    setIsHistoryOpen(false);
    setDeleteConfirm({ type: "one", id });
  };

  const openClearHistoryConfirm = () => {
    setIsHistoryOpen(false);
    setDeleteConfirm({ type: "all" });
  };

  const handleConfirmDelete = async () => {
    const target = deleteConfirm;
    setDeleteConfirm(null);
    if (!target) {
      return;
    }
    if (target.type === "all") {
      await handleClearHistory();
      return;
    }
    await handleDeleteConversation(target.id);
  };

  const appendReferenceImages = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }

    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );

      setComposerMode("image");
      setReferenceImages((prev) => [...prev, ...previews]);
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取参考图失败";
      toast.error(message);
    }
  }, []);

  const handleReferenceImageChange = useCallback(
    async (files: File[]) => {
      if (files.length === 0) {
        return;
      }

      await appendReferenceImages(files);
    },
    [appendReferenceImages],
  );

  const handleRemoveReferenceImage = useCallback((index: number) => {
    setReferenceImages((prev) => {
      const next = prev.filter((_, currentIndex) => currentIndex !== index);
      if (next.length === 0 && fileInputRef.current) {
        fileInputRef.current.value = "";
      }
      return next;
    });
  }, []);

  const handleContinueEdit = useCallback(
    async (conversationId: string, image: StoredImage | StoredReferenceImage) => {
      try {
        const nextReference =
          "dataUrl" in image
            ? {
                referenceImage: image,
              }
            : await buildReferenceImageFromStoredImage(image, `conversation-${conversationId}-${Date.now()}.png`);
        if (!nextReference) {
          return;
        }

        setSelectedConversationId(conversationId);
        setComposerMode("image");
        setReferenceImages((prev) => [
          ...prev,
          {
            ...nextReference.referenceImage,
            source: "conversation",
          },
        ]);
        setImagePrompt("");
        textareaRef.current?.focus();
        toast.success("已加入当前参考图，继续输入描述即可编辑");
      } catch (error) {
        const message = error instanceof Error ? error.message : "读取结果图失败";
        toast.error(message);
      }
    },
    [],
  );

  const openLightbox = useCallback((images: ImageLightboxItem[], index: number) => {
    if (images.length === 0) {
      return;
    }

    setLightboxImages(images);
    setLightboxIndex(Math.max(0, Math.min(index, images.length - 1)));
    setLightboxOpen(true);
  }, []);

  const openEditTurnDialog = useCallback((conversationId: string, turnId: string) => {
    const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
    const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
    if (!targetConversation || !targetTurn) {
      toast.error("未找到对应的对话轮次");
      return;
    }
    if (isTurnInProgress(targetTurn)) {
      toast.error("当前轮次正在处理，稍后再编辑");
      return;
    }
    setEditingTurnDraft({
      conversationId,
      turnId,
      prompt: targetTurn.prompt,
      model:
        targetTurn.mode === "chat"
          ? isChatModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_CHAT_MODEL
          : isImageTaskModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_IMAGE_MODEL,
      mode: targetTurn.mode,
      count: targetTurn.mode === "chat" ? "1" : String(normalizeRequestedImageCount(targetTurn.count || targetTurn.images.length || 1)),
      size: targetTurn.mode === "chat" ? "" : targetTurn.size,
      quality: targetTurn.quality || DEFAULT_IMAGE_QUALITY,
      referenceImages: targetTurn.mode === "chat" ? [] : targetTurn.referenceImages,
    });
  }, []);

  const handleEditReferenceImageChange = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );
      setEditingTurnDraft((current) =>
        current
          ? {
              ...current,
              referenceImages: [...current.referenceImages, ...previews],
            }
          : current,
      );
      if (editFileInputRef.current) {
        editFileInputRef.current.value = "";
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取参考图失败";
      toast.error(message);
    }
  }, []);

  const handleRemoveEditReferenceImage = useCallback((index: number) => {
    setEditingTurnDraft((current) =>
      current
        ? {
            ...current,
            referenceImages: current.referenceImages.filter((_, currentIndex) => currentIndex !== index),
          }
        : current,
    );
  }, []);

  const runConversationQueue = useCallback(
    async (conversationId: string) => {
      if (activeConversationQueueIds.has(conversationId)) {
        return;
      }

      const snapshot = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const activeTurn = snapshot?.turns.find(
        (turn) =>
          (turn.status === "queued" || turn.status === "generating") &&
          turn.images.some((image) => image.status === "loading"),
      );
      if (!snapshot || !activeTurn) {
        return;
      }

      activeConversationQueueIds.add(conversationId);
      const activeTurnKey = imageTurnProgressKey(conversationId, activeTurn.id);
      updateTurnProgress(conversationId, activeTurn.id, {
        message: activeTurn.mode === "chat" ? "正在准备对话请求" : "正在准备生成任务",
        detail:
          activeTurn.mode === "chat"
            ? "正在整理上下文"
            : `准备处理 ${activeTurn.images.filter((image) => image.status === "loading").length || activeTurn.count} 张图片`,
      });
      const applyTasks = async (tasks: ImageTask[]) => {
        const taskMap = new Map(tasks.map((task) => [task.id, task]));
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          let completedActiveTurn = false;
          const turns = conversation.turns.map((turn) => {
            if (turn.id !== activeTurn.id) {
              return turn;
            }
            const images = turn.images.map((image, imageIndex) => {
              const taskId = image.taskId || image.id;
              const task = taskMap.get(taskId);
              const taskImage = image.taskId === taskId ? image : { ...image, taskId };
              return task ? taskDataToStoredImage(taskImage, task, imageDataIndexForTask(turn.images, imageIndex)) : image;
            });
            const derived = deriveTurnStatus({ ...turn, status: "generating", images });
            const nextTurn = {
              ...turn,
              ...derived,
              images,
            };
            if (isTurnInProgress(turn) && !isTurnInProgress(nextTurn)) {
              completedActiveTurn = true;
            }
            return nextTurn;
          });
          const nextConversation = {
            ...conversation,
            turns,
          };
          return completedActiveTurn
            ? {
                ...nextConversation,
                updatedAt: new Date().toISOString(),
              }
            : nextConversation;
        });
      };

      try {
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          return {
            ...conversation,
            turns: conversation.turns.map((turn) =>
              turn.id === activeTurn.id
                ? {
                    ...turn,
                    status: "generating",
                    error: undefined,
                    images: turn.images.map((image, imageIndex) =>
                      image.status === "loading"
                        ? {
                            ...image,
                            taskId:
                              activeTurn.mode === "chat"
                                ? undefined
                                : imageTaskIdForImage(turn.id, turn.images, imageIndex),
                          }
                        : image,
                    ),
                  }
                : turn,
            ),
          };
        });

        if (activeTurn.mode === "chat") {
          updateTurnProgress(conversationId, activeTurn.id, {
            message: "正在请求对话回复",
            detail: "请求已提交，等待模型返回文本",
          });
          const response = await createChatCompletion(activeTurn.model, buildImageTaskMessages(snapshot, activeTurn.id));
          if (cancelledTurnIdsRef.current.has(activeTurnKey)) {
            return;
          }
          const text = chatCompletionResponseText(response);
          if (!text) {
            throw new Error("模型没有返回文本内容");
          }
          await updateConversation(conversationId, (current) => {
            const conversation = current ?? snapshot;
            return {
              ...conversation,
              updatedAt: new Date().toISOString(),
              turns: conversation.turns.map((turn) => {
                if (turn.id !== activeTurn.id) {
                  return turn;
                }
                const images = turn.images.map((image) =>
                  image.status === "loading"
                    ? {
                        ...image,
                        taskId: undefined,
                        status: "message" as const,
                        text_response: text,
                        error: undefined,
                      }
                    : image,
                );
                return {
                  ...turn,
                  ...deriveTurnStatus({ ...turn, images }),
                  images,
                };
              }),
            };
          });
          updateTurnProgress(conversationId, activeTurn.id, {
            message: "回复完成",
            detail: "正在刷新会话",
          });
          return;
        }

        updateTurnProgress(conversationId, activeTurn.id, {
          message: usesReferenceImages(activeTurn.mode) ? "正在整理参考图" : "正在准备生成请求",
          detail: usesReferenceImages(activeTurn.mode) ? "正在读取参考图并准备上传" : "正在创建图片生成任务",
        });
        const referenceFiles = activeTurn.referenceImages.map((image, index) =>
          dataUrlToFile(image.dataUrl, image.name || `${activeTurn.id}-${index + 1}.png`, image.type),
        );
        if (usesReferenceImages(activeTurn.mode) && referenceFiles.length === 0) {
          throw new Error("未找到可用的参考图");
        }
        const taskMessages = buildImageTaskMessages(snapshot, activeTurn.id);
        const pendingTaskGroups = activeTurn.images.reduce<Array<{ taskId: string; count: number }>>(
          (groups, image, imageIndex) => {
            if (image.status !== "loading") {
              return groups;
            }
            const taskId = imageTaskIdForImage(activeTurn.id, activeTurn.images, imageIndex);
            const existing = groups.find((group) => group.taskId === taskId);
            if (existing) {
              existing.count += 1;
            } else {
              groups.push({ taskId, count: 1 });
            }
            return groups;
          },
          [],
        );
        updateTurnProgress(conversationId, activeTurn.id, {
          message: "正在提交生成请求",
          detail: `${pendingTaskGroups.length} 个图片任务正在入队`,
        });
        const submitted = await Promise.all(
          pendingTaskGroups.map((group) =>
            usesReferenceImages(activeTurn.mode)
              ? createImageEditTask(
                  group.taskId,
                  referenceFiles,
                  activeTurn.prompt,
                  activeTurn.model,
                  activeTurn.size,
                  activeTurn.quality || DEFAULT_IMAGE_QUALITY,
                  group.count,
                  taskMessages,
                )
              : createImageGenerationTask(
                  group.taskId,
                  activeTurn.prompt,
                  activeTurn.model,
                  activeTurn.size,
                  activeTurn.quality || DEFAULT_IMAGE_QUALITY,
                  group.count,
                  taskMessages,
                ),
          ),
        );
        await applyTasks(submitted);
        updateTurnProgress(conversationId, activeTurn.id, {
          message: "等待生成结果",
          detail: "请求已提交，正在轮询任务状态",
        });

        while (true) {
          const latestConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
          const latestTurn = latestConversation?.turns.find((turn) => turn.id === activeTurn.id);
          const loadingTaskIds = Array.from(
            new Set(
              latestTurn?.images.flatMap((image) =>
                image.status === "loading" && image.taskId ? [image.taskId] : [],
              ) || [],
            ),
          );
          if (loadingTaskIds.length === 0) {
            break;
          }

          updateTurnProgress(conversationId, activeTurn.id, {
            message: "等待生成结果",
            detail: `还有 ${loadingTaskIds.length} 张图片处理中`,
          });
          await sleep(2000);
          const taskList = await fetchImageTasks(loadingTaskIds);
          if (taskList.items.length > 0) {
            await applyTasks(taskList.items);
          }
          if (taskList.missing_ids.length > 0 && latestTurn) {
            updateTurnProgress(conversationId, activeTurn.id, {
              message: "正在恢复生成任务",
              detail: `${taskList.missing_ids.length} 个任务状态丢失，正在重新提交`,
            });
            const missingTaskGroups = taskList.missing_ids.flatMap((taskId) => {
              const count = latestTurn.images.filter((image) => image.status === "loading" && image.taskId === taskId).length;
              return count > 0 ? [{ taskId, count }] : [];
            });
            const resubmitted = await Promise.all(
              missingTaskGroups.map((group) =>
                usesReferenceImages(activeTurn.mode)
                  ? createImageEditTask(
                      group.taskId,
                      referenceFiles,
                      activeTurn.prompt,
                      activeTurn.model,
                      activeTurn.size,
                      activeTurn.quality || DEFAULT_IMAGE_QUALITY,
                      group.count,
                      taskMessages,
                    )
                  : createImageGenerationTask(
                      group.taskId,
                      activeTurn.prompt,
                      activeTurn.model,
                      activeTurn.size,
                      activeTurn.quality || DEFAULT_IMAGE_QUALITY,
                      group.count,
                      taskMessages,
                    ),
              ),
            );
            if (resubmitted.length > 0) {
              await applyTasks(resubmitted);
            }
          }
        }

        updateTurnProgress(conversationId, activeTurn.id, {
          message: "生成完成",
          detail: "正在刷新会话",
        });
        window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
      } catch (error) {
        const message = formatImageTaskError(error, activeTurn.mode === "chat" ? "对话请求失败" : "生成图片失败");
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          return {
            ...conversation,
            updatedAt: new Date().toISOString(),
            turns: conversation.turns.map((turn) =>
              turn.id === activeTurn.id
                ? {
                    ...turn,
                    status: "error",
                    error: message,
                    images: turn.images.map((image) =>
                      image.status === "loading" ? { ...image, status: "error", error: message } : image,
                    ),
                  }
                : turn,
            ),
          };
        });
        toast.error(message);
      } finally {
        clearTurnProgress(conversationId, activeTurn.id);
        cancelledTurnIdsRef.current.delete(activeTurnKey);
        activeConversationQueueIds.delete(conversationId);
        for (const conversation of conversationsRef.current) {
          if (
            !activeConversationQueueIds.has(conversation.id) &&
            conversation.turns.some(
              (turn) =>
                (turn.status === "queued" || turn.status === "generating") &&
                turn.images.some((image) => image.status === "loading"),
            )
          ) {
            void runConversationQueue(conversation.id);
          }
        }
      }
    },
    [clearTurnProgress, updateConversation, updateTurnProgress],
  );
  useEffect(() => {
    for (const conversation of conversations) {
      if (
        !activeConversationQueueIds.has(conversation.id) &&
        conversation.turns.some(
          (turn) =>
            (turn.status === "queued" || turn.status === "generating") &&
            turn.images.some((image) => image.status === "loading"),
        )
      ) {
        void runConversationQueue(conversation.id);
      }
    }
  }, [conversations, runConversationQueue]);

  const handleCancelTurn = useCallback(
    async (conversationId: string, turnId: string) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      const taskIds = Array.from(
        new Set(targetTurn.images.flatMap((image) => (image.status === "loading" && image.taskId ? [image.taskId] : []))),
      );
      if (taskIds.length === 0) {
        if (targetTurn.mode === "chat") {
          const turnKey = imageTurnProgressKey(conversationId, turnId);
          cancelledTurnIdsRef.current.add(turnKey);
          clearTurnProgress(conversationId, turnId);
          await updateConversation(conversationId, (current) => {
            const conversation = current ?? targetConversation;
            return {
              ...conversation,
              updatedAt: new Date().toISOString(),
              turns: conversation.turns.map((turn) => {
                if (turn.id !== turnId) {
                  return turn;
                }
                const images = turn.images.map((image) =>
                  image.status === "loading"
                    ? {
                        ...image,
                        status: "cancelled" as const,
                        error: "请求已终止",
                      }
                    : image,
                );
                return {
                  ...turn,
                  ...deriveTurnStatus({ ...turn, images }),
                  images,
                };
              }),
            };
          });
          toast.success("已终止对话请求");
        }
        return;
      }

      const results = await Promise.allSettled(taskIds.map((taskId) => cancelImageTask(taskId)));
      const taskMap = new Map(
        results.flatMap((result) => (result.status === "fulfilled" ? [[result.value.id, result.value] as const] : [])),
      );
      const failedRequests = results.filter((result) => result.status === "rejected").length;

      await updateConversation(conversationId, (current) => {
        const conversation = current ?? targetConversation;
        return {
          ...conversation,
          updatedAt: new Date().toISOString(),
          turns: conversation.turns.map((turn) => {
            if (turn.id !== turnId) {
              return turn;
            }
            const images = turn.images.map((image, imageIndex) => {
              if (image.status !== "loading") {
                return image;
              }
              const taskId = image.taskId || image.id;
              const task = taskMap.get(taskId);
              if (task) {
                return taskDataToStoredImage({ ...image, taskId }, task, imageDataIndexForTask(turn.images, imageIndex));
              }
              return {
                ...image,
                taskId,
                status: "cancelled" as const,
                error: failedRequests > 0 ? "终止请求失败，已在本地停止等待" : "任务已终止",
              };
            });
            const derived = deriveTurnStatus({ ...turn, images });
            return {
              ...turn,
              ...derived,
              images,
            };
          }),
        };
      });

      if (failedRequests > 0) {
        toast.error(`部分终止请求失败：${failedRequests}/${taskIds.length}`);
      } else {
        toast.success("已终止生成任务");
      }
    },
    [clearTurnProgress, updateConversation],
  );

  const handleRetryImage = useCallback(
    async (conversationId: string, turnId: string, imageIndex: number) => {
      const retryKey = `${conversationId}:${turnId}:${imageIndex}`;
      if (retryingImageIdsRef.current.has(retryKey)) {
        return;
      }

      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      const targetImage = targetTurn?.images[imageIndex];
      if (!targetConversation || !targetTurn || !targetImage) {
        toast.error("未找到对应的图片记录");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再重试");
        return;
      }
      if (!targetTurn.prompt.trim()) {
        toast.error("请输入提示词");
        return;
      }
      if (targetImage.status !== "error" && targetImage.status !== "message") {
        toast.error("只有失败图片或模型文本回复可以单独重试");
        return;
      }
      if (usesReferenceImages(targetTurn.mode) && targetTurn.referenceImages.length === 0) {
        toast.error("未找到可用的参考图");
        return;
      }

      retryingImageIdsRef.current.add(retryKey);
      const now = new Date().toISOString();
      const retryTaskId = imageTaskBatchId(`${targetTurn.id}-${createId()}`, imageIndex);
      try {
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? targetConversation;
          return {
            ...conversation,
            updatedAt: now,
            turns: conversation.turns.map((turn) => {
              if (turn.id !== turnId) {
                return turn;
              }
              const images = turn.images.map((image, index) =>
                index === imageIndex
                  ? {
                      ...image,
                      taskId: turn.mode === "chat" ? undefined : retryTaskId,
                      status: "loading" as const,
                      b64_json: undefined,
                      url: undefined,
                      revised_prompt: undefined,
                      text_response: undefined,
                      error: undefined,
                    }
                  : image,
              );
              const derived = deriveTurnStatus({ ...turn, status: "queued", images });
              return {
                ...turn,
                ...derived,
                images,
              };
            }),
          };
        });
        void runConversationQueue(conversationId);
        toast.success("已加入重试队列");
      } catch (error) {
        toast.error(formatImageTaskError(error, "提交重试失败"));
      } finally {
        retryingImageIdsRef.current.delete(retryKey);
      }
    },
    [runConversationQueue, updateConversation],
  );

  const handleRegenerateTurn = useCallback(
    async (conversationId: string, turnId: string) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      if (!targetTurn.prompt.trim()) {
        toast.error("请输入提示词");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再重新生成");
        return;
      }
      if (usesReferenceImages(targetTurn.mode) && targetTurn.referenceImages.length === 0) {
        toast.error("未找到可用的参考图");
        return;
      }

      const now = new Date().toISOString();
      const regenerationId = createId();
      await updateConversation(conversationId, (current) => {
        const conversation = current ?? targetConversation;
        const isFirstTurn = conversation.turns[0]?.id === turnId;
        return {
          ...conversation,
          title: isFirstTurn ? buildConversationTitle(targetTurn.prompt) : conversation.title,
          updatedAt: now,
          turns: conversation.turns.map((turn) => {
            if (turn.id !== turnId) {
              return turn;
            }

            const imageCount = turn.mode === "chat" ? 1 : normalizeRequestedImageCount(turn.count || turn.images.length || 1);
            return {
              ...turn,
              count: imageCount,
              status: "queued",
              error: undefined,
              images: Array.from({ length: imageCount }, (_, index) => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: turn.mode === "chat" ? undefined : imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  status: "loading" as const,
                };
              }),
            };
          }),
        };
      });
      void runConversationQueue(conversationId);
      toast.success("已加入重新生成队列");
    },
    [runConversationQueue, updateConversation],
  );

  const handleSaveEditingTurn = useCallback(
    async (regenerate: boolean) => {
      const draft = editingTurnDraft;
      if (!draft) {
        return;
      }
      const prompt = draft.prompt.trim();
      if (!prompt) {
        toast.error("请输入提示词");
        return;
      }

      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === draft.conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === draft.turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再编辑");
        return;
      }

      const imageCount = draft.mode === "chat" ? 1 : normalizeRequestedImageCount(draft.count);
      const mode = draft.mode === "chat" ? "chat" : getComposerConversationMode("image", draft.referenceImages);
      const referenceImages = usesReferenceImages(mode) ? draft.referenceImages : [];
      const now = new Date().toISOString();
      const regenerationId = createId();
      await updateConversation(draft.conversationId, (current) => {
        const conversation = current ?? targetConversation;
        const isFirstTurn = conversation.turns[0]?.id === draft.turnId;
        return {
          ...conversation,
          title: isFirstTurn ? buildConversationTitle(prompt) : conversation.title,
          updatedAt: now,
          turns: conversation.turns.map((turn) => {
            if (turn.id !== draft.turnId) {
              return turn;
            }

            const baseTurn = {
              ...turn,
              prompt,
              model: draft.model,
              mode,
              referenceImages,
              count: imageCount,
              size: mode === "chat" ? "" : draft.size,
              quality: mode === "chat" ? undefined : draft.quality,
            };
            if (!regenerate) {
              return baseTurn;
            }
            return {
              ...baseTurn,
              status: "queued" as const,
              error: undefined,
              images: Array.from({ length: imageCount }, (_, index) => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: mode === "chat" ? undefined : imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  status: "loading" as const,
                };
              }),
            };
          }),
        };
      });

      setEditingTurnDraft(null);
      if (editFileInputRef.current) {
        editFileInputRef.current.value = "";
      }
      if (regenerate) {
        void runConversationQueue(draft.conversationId);
        toast.success("已保存并加入重新生成队列");
      } else {
        toast.success("已保存编辑设置");
      }
    },
    [editingTurnDraft, runConversationQueue, updateConversation],
  );

  const handleSubmit = async () => {
    if (isSubmitDispatchingRef.current) {
      return;
    }

    const prompt = imagePrompt.trim();
    if (!prompt) {
      toast.error("请输入提示词");
      return;
    }
    isSubmitDispatchingRef.current = true;
    let draftProgressTarget: { conversationId: string; turnId: string } | null = null;

    try {
      const effectiveImageMode = getComposerConversationMode(composerMode, referenceImages);
      const effectiveModel =
        effectiveImageMode === "chat"
          ? isChatModel(imageModel)
            ? imageModel
            : DEFAULT_CHAT_MODEL
          : isImageTaskModel(imageModel)
            ? imageModel
            : DEFAULT_IMAGE_MODEL;
      const requestedCount = effectiveImageMode === "chat" ? 1 : parsedCount;

      const targetConversation = selectedConversationId
        ? conversationsRef.current.find((conversation) => conversation.id === selectedConversationId) ?? null
        : null;
      const now = new Date().toISOString();
      const conversationId = targetConversation?.id ?? createId();
      const turnId = createId();
      const draftTurn: ImageTurn = {
        id: turnId,
        prompt,
        model: effectiveModel,
        mode: effectiveImageMode,
        referenceImages: usesReferenceImages(effectiveImageMode) ? referenceImages : [],
        count: requestedCount,
        size: effectiveImageMode === "chat" ? "" : imageSize,
        quality: effectiveImageMode === "chat" ? undefined : imageQuality,
        images: Array.from({ length: requestedCount }, (_, index) => {
          const imageId = `${turnId}-${index}`;
          return {
            id: imageId,
            taskId: effectiveImageMode === "chat" ? undefined : imageTaskBatchId(turnId, index),
            status: "loading" as const,
          };
        }),
        createdAt: now,
        status: "queued",
      };

      const baseConversation: ImageConversation = targetConversation
        ? {
            ...targetConversation,
            updatedAt: now,
            turns: [...targetConversation.turns, draftTurn],
          }
        : {
            id: conversationId,
            title: buildConversationTitle(prompt),
            createdAt: now,
            updatedAt: now,
            turns: [draftTurn],
          };

      draftProgressTarget = { conversationId, turnId };
      updateTurnProgress(conversationId, turnId, {
        message: "正在创建本地记录",
        detail: effectiveImageMode === "chat" ? "正在保存对话内容" : "正在保存提示词和生成参数",
      });
      setSelectedConversationId(conversationId);
      clearComposerInputs();

      await persistConversation(baseConversation);
      void runConversationQueue(conversationId);

      const targetStats = getImageConversationStats(baseConversation);
      if (targetStats.running > 0 || targetStats.queued > 1) {
        toast.success("已加入当前对话队列");
      } else if (!targetConversation) {
        toast.success(effectiveImageMode === "chat" ? "已创建新对话并发送" : "已创建新对话并开始处理");
      } else {
        toast.success("已发送到当前对话");
      }
    } catch (error) {
      if (draftProgressTarget) {
        clearTurnProgress(draftProgressTarget.conversationId, draftProgressTarget.turnId);
      }
      toast.error(formatImageTaskError(error, "提交任务失败"));
    } finally {
      isSubmitDispatchingRef.current = false;
    }
  };

  return (
    <>
      <section className="mx-auto grid h-[calc(100dvh-6.25rem)] min-h-0 w-full max-w-[1380px] grid-cols-1 gap-2 px-0 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] sm:h-[calc(100dvh-5rem)] sm:gap-3 sm:px-3 sm:pb-6 lg:grid-cols-[240px_minmax(0,1fr)]">
        <div className="hidden h-full min-h-0 border-r border-[#f2f3f5] pr-3 lg:block">
          <ImageSidebar
            conversations={conversations}
            isLoadingHistory={isLoadingHistory}
            selectedConversationId={selectedConversationId}
            onCreateDraft={handleCreateDraft}
            onClearHistory={openClearHistoryConfirm}
            onSelectConversation={setSelectedConversationId}
            onDeleteConversation={openDeleteConversationConfirm}
            formatConversationTime={formatConversationTime}
          />
        </div>

        <Dialog open={isHistoryOpen} onOpenChange={setIsHistoryOpen}>
          <DialogContent className="flex h-[min(82dvh,760px)] w-[92vw] max-w-[460px] flex-col overflow-hidden rounded-[32px] border-white/80 bg-white p-0 shadow-[0_32px_110px_-38px_rgba(15,23,42,0.45)] sm:rounded-[36px]">
            <DialogHeader className="px-6 pt-7 pb-4 sm:px-8">
              <DialogTitle className="flex items-center gap-2 text-xl font-bold tracking-tight">
                <History className="size-5" />
                历史记录
              </DialogTitle>
            </DialogHeader>
            <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-8 sm:px-8">
              <ImageSidebar
                conversations={conversations}
                isLoadingHistory={isLoadingHistory}
                selectedConversationId={selectedConversationId}
                onCreateDraft={() => {
                  handleCreateDraft();
                  setIsHistoryOpen(false);
                }}
                onClearHistory={openClearHistoryConfirm}
                onSelectConversation={(id) => {
                  setSelectedConversationId(id);
                  setIsHistoryOpen(false);
                }}
                onDeleteConversation={openDeleteConversationConfirm}
                formatConversationTime={formatConversationTime}
                hideActionButtons
              />
            </div>
          </DialogContent>
        </Dialog>

        {editingTurnDraft ? (
          <Dialog open onOpenChange={(open) => (!open ? setEditingTurnDraft(null) : null)}>
            <DialogContent className="flex max-h-[88dvh] w-[min(92vw,640px)] flex-col overflow-hidden rounded-[28px] p-0">
              <DialogHeader className="px-6 pt-6 pb-2">
                <DialogTitle>{editingTurnDraft.mode === "chat" ? "编辑对话" : "编辑生成设置"}</DialogTitle>
                <DialogDescription>
                  {editingTurnDraft.mode === "chat" ? "修改本轮消息和对话模型。" : "修改本轮提示词、参考图和生成参数。"}
                </DialogDescription>
              </DialogHeader>
              <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
                <div className="flex flex-col gap-5">
                  <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                    提示词
                    <Textarea
                      value={editingTurnDraft.prompt}
                      onChange={(event) =>
                        setEditingTurnDraft((current) =>
                          current ? { ...current, prompt: event.target.value } : current,
                        )
                      }
                      className="min-h-[128px] resize-y rounded-2xl border-stone-200 bg-white text-sm leading-6 shadow-none"
                    />
                  </label>

                  {editingTurnDraft.mode !== "chat" ? (
                  <div className="flex flex-col gap-3">
                    <input
                      ref={editFileInputRef}
                      type="file"
                      accept="image/*"
                      multiple
                      className="hidden"
                      onChange={(event) => {
                        void handleEditReferenceImageChange(Array.from(event.target.files || []));
                      }}
                    />
                    <div className="flex items-center justify-between gap-3">
                      <div className="text-sm font-medium text-stone-700">参考图</div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="rounded-full border-stone-200 bg-white"
                        onClick={() => editFileInputRef.current?.click()}
                      >
                        <ImagePlus className="size-4" />
                        上传图片
                      </Button>
                    </div>
                    {editingTurnDraft.referenceImages.length > 0 ? (
                      <div className="flex flex-wrap gap-2">
                        {editingTurnDraft.referenceImages.map((image, index) => (
                          <div key={`${image.name}-${index}`} className="relative size-20 shrink-0">
                            <button
                              type="button"
                              className="size-20 overflow-hidden rounded-2xl border border-stone-200 bg-stone-100"
                              onClick={() =>
                                openLightbox(
                                  editingTurnDraft.referenceImages.map((item, itemIndex) => ({
                                    id: `${item.name}-${itemIndex}`,
                                    src: item.dataUrl,
                                  })),
                                  index,
                                )
                              }
                              aria-label={`预览参考图 ${image.name || index + 1}`}
                            >
                              <img
                                src={image.dataUrl}
                                alt={image.name || `参考图 ${index + 1}`}
                                className="h-full w-full object-cover"
                              />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleRemoveEditReferenceImage(index)}
                              className="absolute -top-1 -right-1 inline-flex size-6 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 transition hover:text-stone-900"
                              aria-label={`移除参考图 ${image.name || index + 1}`}
                            >
                              <X className="size-3.5" />
                            </button>
                          </div>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  ) : null}

                  <div className={cn("grid grid-cols-1 gap-3", editingTurnDraft.mode === "chat" ? "sm:grid-cols-1" : "sm:grid-cols-[120px_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)]")}>
                    {editingTurnDraft.mode !== "chat" ? (
                    <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                      张数
                      <Input
                        type="number"
                        inputMode="numeric"
                        min="1"
                        max="10"
                        step="1"
                        value={editingTurnDraft.count}
                        onChange={(event) =>
                          setEditingTurnDraft((current) =>
                            current ? { ...current, count: event.target.value } : current,
                          )
                        }
                      />
                    </label>
                    ) : null}
                    <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                      模型
                      <Select
                        value={editingTurnDraft.model}
                        onValueChange={(value) =>
                          setEditingTurnDraft((current) =>
                            current && isImageModel(value) ? { ...current, model: value } : current,
                          )
                        }
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            {(editingTurnDraft.mode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_TASK_MODEL_OPTIONS).map((option) => (
                              <SelectItem key={option.value} value={option.value}>
                                {option.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </label>
                    {editingTurnDraft.mode !== "chat" ? (
                    <>
                    <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                      比例
                      <Select
                        value={editingTurnDraft.size || EMPTY_IMAGE_SIZE_SELECT_VALUE}
                        onValueChange={(value) =>
                          setEditingTurnDraft((current) =>
                            current
                              ? {
                                  ...current,
                                  size: value === EMPTY_IMAGE_SIZE_SELECT_VALUE ? "" : value,
                                }
                              : current,
                          )
                        }
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            {IMAGE_SIZE_OPTIONS.map((option) => (
                              <SelectItem
                                key={option.label}
                                value={option.value || EMPTY_IMAGE_SIZE_SELECT_VALUE}
                              >
                                {option.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </label>
                    <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                      质量
                      <Select
                        value={editingTurnDraft.quality}
                        onValueChange={(value) =>
                          setEditingTurnDraft((current) =>
                            current && isImageQuality(value) ? { ...current, quality: value } : current,
                          )
                        }
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            {IMAGE_QUALITY_OPTIONS.map((option) => (
                              <SelectItem key={option.value} value={option.value}>
                                {option.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </label>
                    </>
                    ) : null}
                  </div>
                </div>
              </div>
              <DialogFooter className="border-t border-stone-100 px-6 py-4">
                <Button variant="outline" onClick={() => setEditingTurnDraft(null)}>
                  取消
                </Button>
                <Button variant="outline" onClick={() => void handleSaveEditingTurn(false)}>
                  保存
                </Button>
                <Button onClick={() => void handleSaveEditingTurn(true)}>
                  {editingTurnDraft.mode === "chat" ? "保存并重新发送" : "保存并重新生成"}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        ) : null}

        <div className="relative flex min-h-0 flex-col gap-2 sm:gap-4">
          <div className="flex items-center justify-between gap-2 px-1 sm:px-4">
            <div className="flex min-w-0 flex-1 items-center gap-2 lg:hidden">
              <Button
                variant="outline"
                className="h-10 min-w-0 flex-1 shrink rounded-full border-[#e5e7eb] bg-white text-[#45515e] shadow-sm"
                onClick={() => setIsHistoryOpen(true)}
              >
                <History className="size-4" />
                <span className="truncate">历史记录 ({conversations.length})</span>
              </Button>
              <Button
                className="h-10 rounded-full shadow-sm"
                onClick={handleCreateDraft}
              >
                <Plus className="size-4" />
                新建
              </Button>
              <Button
                variant="outline"
                className="h-10 rounded-full border-[#e5e7eb] bg-white px-3 text-[#45515e] shadow-sm"
                onClick={openClearHistoryConfirm}
                disabled={conversations.length === 0}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
            <div className="flex shrink-0 items-center gap-2 lg:absolute lg:top-0 lg:right-4 lg:z-20">
              {activeTaskCount > 0 ? (
                <div className="flex h-10 shrink-0 items-center gap-1.5 rounded-full bg-amber-50 px-3 text-xs font-medium text-amber-700 ring-1 ring-amber-100">
                  <LoaderCircle className="size-3.5 animate-spin" />
                  {activeTaskCount}<span className="hidden sm:inline"> 个任务处理中</span>
                </div>
              ) : null}
              <AnnouncementNotifications target="image" className="shrink-0" />
            </div>
          </div>

          <div
            ref={resultsViewportRef}
            className="hide-scrollbar min-h-0 flex-1 overflow-y-auto px-1 pt-2 pb-[14rem] sm:px-4 sm:pt-4 sm:pb-[15rem]"
            style={composerDockHeight > 0 ? { paddingBottom: composerDockHeight + 24 } : undefined}
          >
            <ImageResults
              selectedConversation={selectedConversation}
              progressByTurnKey={progressByTurnKey}
              progressNow={progressNow}
              promptPresets={IMAGE_PROMPT_PRESETS}
              onOpenLightbox={openLightbox}
              onApplyPromptPreset={handleApplyPromptPreset}
              onContinueEdit={handleContinueEdit}
              onEditTurn={openEditTurnDialog}
              onCancelTurn={handleCancelTurn}
              onRegenerateTurn={handleRegenerateTurn}
              onRetryImage={handleRetryImage}
              formatConversationTime={formatConversationTime}
            />
          </div>

          <div
            ref={composerDockRef}
            className="pointer-events-none absolute inset-x-0 bottom-0 z-30 px-1 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] sm:px-4 sm:pb-2"
            style={
              {
                "--image-composer-dock-height": `${composerDockHeight}px`,
              } as CSSProperties
            }
          >
            <div className="pointer-events-auto mx-auto w-full max-w-[900px]">
              <ImageComposer
                composerMode={composerMode}
                prompt={imagePrompt}
                imageCount={imageCount}
                imageModel={imageModel}
                imageModelOptions={composerModelOptions}
                imageSize={imageSize}
                imageQuality={imageQuality}
                imageQualityOptions={IMAGE_QUALITY_OPTIONS}
                imageOutputHint={imageOutputHint}
                referenceImages={referenceImages}
                textareaRef={textareaRef}
                fileInputRef={fileInputRef}
                onComposerModeChange={handleComposerModeChange}
                onPromptChange={setImagePrompt}
                onImageCountChange={setImageCount}
                onImageModelChange={setImageModel}
                onImageSizeChange={setImageSize}
                onImageQualityChange={setImageQuality}
                onSubmit={handleSubmit}
                onPickReferenceImage={() => fileInputRef.current?.click()}
                onOpenPromptMarket={() => setIsPromptMarketOpen(true)}
                onReferenceImageChange={handleReferenceImageChange}
                onRemoveReferenceImage={handleRemoveReferenceImage}
              />
            </div>
          </div>
        </div>
      </section>

      <ImagePromptMarket
        open={isPromptMarketOpen}
        onOpenChange={setIsPromptMarketOpen}
        onApplyPrompt={handleApplyMarketPrompt}
      />

      <ImageLightbox
        images={lightboxImages}
        currentIndex={lightboxIndex}
        open={lightboxOpen}
        onOpenChange={setLightboxOpen}
        onIndexChange={setLightboxIndex}
      />

      {deleteConfirm ? (
        <Dialog open onOpenChange={(open) => (!open ? setDeleteConfirm(null) : null)}>
          <DialogContent showCloseButton={false} className="rounded-2xl p-6">
            <DialogHeader className="gap-2">
              <DialogTitle>{deleteConfirmTitle}</DialogTitle>
              <DialogDescription className="text-sm leading-6">
                {deleteConfirmDescription}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="outline" onClick={() => setDeleteConfirm(null)}>
                取消
              </Button>
              <Button className="bg-rose-600 text-white hover:bg-rose-700" onClick={() => void handleConfirmDelete()}>
                确认删除
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </>
  );
}

export default function ImagePage() {
  const { isCheckingAuth, session } = useAuthGuard();

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return <ImagePageContent />;
}
