"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { History, ImagePlus, LoaderCircle, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { ImageComposer } from "@/app/image/components/image-composer";
import { ImagePromptMarket } from "@/app/image/components/image-prompt-market";
import { ImageResults, type ImageLightboxItem } from "@/app/image/components/image-results";
import type { BananaPrompt } from "@/app/image/banana-prompts";
import {
  CUSTOM_IMAGE_ASPECT_RATIO,
  DEFAULT_IMAGE_CUSTOM_HEIGHT,
  DEFAULT_IMAGE_CUSTOM_RATIO,
  DEFAULT_IMAGE_CUSTOM_WIDTH,
  IMAGE_ASPECT_RATIO_OPTIONS,
  IMAGE_RESOLUTION_OPTIONS,
  IMAGE_SIZE_MODE_OPTIONS,
  buildImageSize,
  formatImageSizeDisplay,
  getImageSizeSelectionFromSize,
  getImageSizeRequirementLabel,
  isHighResolutionImageSize,
  isImageAspectRatio,
  isImageResolution,
  isImageSizeMode,
  parseImageRatio,
  type ImageAspectRatio,
  type ImageResolution,
  type ImageSizeMode,
  type ImageSizeSelection,
} from "@/app/image/image-options";
import { IMAGE_PROMPT_PRESETS, type ImagePromptPreset } from "@/app/image/image-presets";
import { ImageSidebar } from "@/app/image/components/image-sidebar";
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
  cancelCreationTask,
  CHAT_MODEL_OPTIONS,
  createChatCompletionTask,
  createImageEditTask,
  createImageGenerationTask,
  DEFAULT_CHAT_MODEL,
  DEFAULT_IMAGE_MODEL,
  fetchCreationTasks,
  fetchProfile,
  IMAGE_CREATION_MODEL_OPTIONS,
  IMAGE_MODEL_ROUTE_DETAILS,
  IMAGE_OUTPUT_FORMAT_OPTIONS,
  isChatModel,
  isImageCreationModel,
  isImageModel,
  isImageOutputFormat,
  supportsImageOutputCompression,
  supportsImageOutputControls,
  supportsStructuredImageParameters,
  usesOfficialImageRoute,
  updateManagedImageVisibility,
  type ImageModel,
  type ImageOutputFormat,
  type CreationTask,
  type CreationTaskMessage,
  type ImageVisibility,
} from "@/lib/api";
import { fetchAuthenticatedImageBlob } from "@/lib/authenticated-image";
import { clearImageManagerCache } from "@/lib/image-manager-cache";
import { getManagedImagePathFromUrl } from "@/lib/image-path";
import { authSessionFromLoginResponse, setVerifiedAuthSession } from "@/lib/session";
import { cn } from "@/lib/utils";
import { useAuthGuard } from "@/lib/use-auth-guard";
import {
  ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY,
  clearImageConversations,
  deleteImageConversation,
  getImageConversationStats,
  getImageTurnLoadingCounts,
  IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT,
  IMAGE_CONVERSATIONS_CHANGED_EVENT,
  listImageConversations,
  saveImageConversation,
  saveImageConversations,
  type ImageConversation,
  type ImageConversationMode,
  type ImageTurn,
  type ImageTurnStatus,
  type StoredImageSizeSelection,
  type StoredImage,
  type StoredReferenceImage,
} from "@/store/image-conversations";
import {
  clearImageTurnProgress,
  getImageTurnProgressSnapshot,
  imageTurnStartedAtTimestamp,
  imageTurnProgressKey,
  setImageTurnProgress,
  subscribeImageTurnProgress,
  type ImageTurnProgress,
} from "@/store/image-turn-progress";

const COMPOSER_MODE_STORAGE_KEY = "chatgpt2api:image_composer_mode";
const IMAGE_MODEL_STORAGE_KEY = "chatgpt2api:image_last_model";
const IMAGE_SIZE_STORAGE_KEY = "chatgpt2api:image_last_size";
const IMAGE_SIZE_MODE_STORAGE_KEY = "chatgpt2api:image_last_size_mode";
const IMAGE_ASPECT_RATIO_STORAGE_KEY = "chatgpt2api:image_last_aspect_ratio";
const IMAGE_RESOLUTION_STORAGE_KEY = "chatgpt2api:image_last_resolution";
const IMAGE_CUSTOM_RATIO_STORAGE_KEY = "chatgpt2api:image_last_custom_ratio";
const IMAGE_CUSTOM_WIDTH_STORAGE_KEY = "chatgpt2api:image_last_custom_width";
const IMAGE_CUSTOM_HEIGHT_STORAGE_KEY = "chatgpt2api:image_last_custom_height";
const IMAGE_OUTPUT_FORMAT_STORAGE_KEY = "chatgpt2api:image_last_output_format";
const IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY = "chatgpt2api:image_last_output_compression";
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";
const DEFAULT_IMAGE_OUTPUT_FORMAT: ImageOutputFormat = "png";
const activeConversationQueueIds = new Set<string>();
const EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE = "__empty_aspect_ratio__";
const MISSING_RECOVERABLE_TASK_ID_ERROR = "页面刷新或任务中断，未找到可恢复的任务 ID";

type ComposerMode = "chat" | "image";

type EditingTurnDraft = {
  conversationId: string;
  turnId: string;
  prompt: string;
  model: ImageModel;
  mode: ImageConversationMode;
  count: string;
  sizeMode: ImageSizeMode;
  aspectRatio: ImageAspectRatio;
  resolution: ImageResolution;
  customRatio: string;
  customWidth: string;
  customHeight: string;
  outputFormat: ImageOutputFormat;
  outputCompression: string;
  visibility: ImageVisibility;
  referenceImages: StoredReferenceImage[];
};

type CreationTaskDataItem = NonNullable<CreationTask["data"]>[number];

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

function imageFileExtensionForOutputFormat(format?: ImageOutputFormat) {
  return format === "jpeg" ? "jpg" : format || "png";
}

function imageMimeTypeForOutputFormat(format?: ImageOutputFormat) {
  return format === "jpeg" ? "image/jpeg" : `image/${format || "png"}`;
}

function buildReferenceImageFromResult(image: StoredImage, fileName: string): StoredReferenceImage | null {
  if (!image.b64_json) {
    return null;
  }
  const mimeType = imageMimeTypeForOutputFormat(image.outputFormat);

  return {
    name: fileName,
    type: mimeType,
    dataUrl: `data:${mimeType};base64,${image.b64_json}`,
  };
}

async function fetchImageAsFile(url: string, fileName: string) {
  const blob = await fetchAuthenticatedImageBlob(url);
  return new File([blob], fileName, { type: blob.type || "image/png" });
}

function buildReferenceFileName(url: string, index: number, fallbackPrefix: string) {
  const path = url.split(/[?#]/, 1)[0] || "";
  const rawName = path.split("/").filter(Boolean).pop() || "";
  let name = rawName;
  try {
    name = rawName ? decodeURIComponent(rawName) : "";
  } catch {
    name = rawName;
  }
  if (name) {
    return name.includes(".") ? name : `${name}.png`;
  }
  return `${fallbackPrefix}-${index + 1}.png`;
}

async function buildReferenceImageFromUrl(
  url: string,
  index: number,
  fallbackPrefix: string,
): Promise<StoredReferenceImage> {
  const file = await fetchImageAsFile(url, buildReferenceFileName(url, index, fallbackPrefix));
  return {
    name: file.name,
    type: file.type || "image/png",
    dataUrl: await readFileAsDataUrl(file),
    source: "upload",
  };
}

function getPromptReferenceImageUrls(prompt: BananaPrompt) {
  const urls = prompt.referenceImageUrls.length > 0 ? prompt.referenceImageUrls : [prompt.preview];
  return Array.from(new Set(urls.map((url) => url.trim()).filter(Boolean)));
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

const IMAGE_TASK_IMAGE_COUNT = 4;

function normalizeRequestedImageCount(value: string | number) {
  return Math.max(1, Math.min(10, Number(value) || 1));
}

function isInvalidCustomRatioSelection(sizeMode: ImageSizeMode, aspectRatio: ImageAspectRatio, customRatio: string) {
  return sizeMode === "ratio" && aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO && !parseImageRatio(customRatio);
}

function effectiveImageSizeSelection(model: ImageModel, selection: ImageSizeSelection): ImageSizeSelection {
  if (supportsStructuredImageParameters(model)) {
    return selection;
  }
  if (selection.mode !== "ratio") {
    return {
      ...selection,
      mode: "auto",
      resolution: "auto",
    };
  }
  return {
    ...selection,
    resolution: "auto",
  };
}

function buildEffectiveImageSizeRequest(model: ImageModel, selection: ImageSizeSelection) {
  const effectiveSelection = effectiveImageSizeSelection(model, selection);
  return {
    selection: effectiveSelection,
    size: buildImageSize(effectiveSelection),
  };
}

function imageOutputFormatForModel(model: ImageModel, format: ImageOutputFormat) {
  return supportsImageOutputControls(model) ? format : undefined;
}

function imageOutputCompressionForModel(model: ImageModel, format: ImageOutputFormat, value: unknown) {
  if (!supportsImageOutputControls(model)) {
    return undefined;
  }
  return imageOutputCompressionForFormat(format, value);
}

function positiveDimension(value: unknown) {
  const dimension = Number(value);
  return Number.isFinite(dimension) && dimension > 0 ? Math.round(dimension) : undefined;
}

function normalizeOutputCompressionValue(value: unknown): number | undefined {
  if (value === undefined || value === null || String(value).trim() === "") {
    return undefined;
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric < 0) {
    return undefined;
  }
  return Math.min(100, Math.round(numeric));
}

function imageOutputCompressionForFormat(format: ImageOutputFormat, value: unknown) {
  if (!supportsImageOutputCompression(format)) {
    return undefined;
  }
  return normalizeOutputCompressionValue(value);
}

function formatHighResolutionHint(canInspectAccounts: boolean) {
  return canInspectAccounts
    ? "Codex 结构化高分辨率参数不会在本地预拦截，会直接提交给上游；上游会按账号能力判断或返回错误。"
    : "Codex 结构化高分辨率参数会直接提交给上游；上游会按账号能力判断或返回错误。";
}

function imageTaskProgressMessage(turn: ImageTurn, elapsedSeconds = 0) {
  if (turn.status === "queued") {
    return turn.mode === "chat"
      ? {
          message: "等待创作并发额度",
          detail: "对话任务已入队，等待可用额度",
        }
      : {
          message: "等待创作并发额度",
          detail: "图片任务已入队，等待可用额度",
        };
  }

  if (turn.mode === "chat") {
    return {
      message: "等待对话回复",
      detail: "对话任务处理中",
    };
  }

  const route = IMAGE_MODEL_ROUTE_DETAILS[turn.model];
  const isHighResolution = supportsStructuredImageParameters(turn.model) && isHighResolutionImageSize(turn.size);
  void elapsedSeconds;
  if (isHighResolution) {
    return {
      message: "高分辨率生成中",
      detail: `${getImageSizeRequirementLabel(turn.size)}，后端已提交给上游等待结果`,
    };
  }
  return {
    message: route ? `${route.routeLabel}生成中` : "等待生成结果",
    detail: "后端正在轮询任务状态",
  };
}

function imageTaskLoadingDetail(turn: ImageTurn, fallbackDetail: string) {
  const counts = getImageTurnLoadingCounts(turn);
  if (turn.mode === "chat") {
    return fallbackDetail;
  }
  if (counts.queued > 0) {
    return `${fallbackDetail}；还有 ${counts.queued} 张图片排队中`;
  }
  if (counts.running > 0) {
    return `${fallbackDetail}；还有 ${counts.running} 张图片处理中`;
  }
  return "图片结果已返回，正在确认任务状态";
}

function imageTaskBatchId(turnId: string, imageIndex: number) {
  return `${turnId}-task-${Math.floor(imageIndex / IMAGE_TASK_IMAGE_COUNT)}`;
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
  "taskStatus",
  "status",
  "path",
  "visibility",
  "b64_json",
  "url",
  "width",
  "height",
  "resolution",
  "outputFormat",
  "revised_prompt",
  "error",
  "text_response",
];

function updateStoredImage(image: StoredImage, updates: Partial<StoredImage>): StoredImage {
  const next = { ...image, ...updates };
  return STORED_IMAGE_FIELDS.every((field) => image[field] === next[field]) ? image : next;
}

function creationTaskImageStatus(task: CreationTask, dataIndex = 0): "queued" | "running" | "success" | "error" | "cancelled" | undefined {
  const outputStatus = task.output_statuses?.[dataIndex];
  if (outputStatus === "queued" || outputStatus === "running" || outputStatus === "success" || outputStatus === "error" || outputStatus === "cancelled") {
    return outputStatus;
  }
  if (task.status === "queued" || task.status === "running" || task.status === "success" || task.status === "error" || task.status === "cancelled") {
    return task.status;
  }
  return undefined;
}

function taskDataToStoredImage(image: StoredImage, task: CreationTask, dataIndex = 0, fallbackVisibility?: ImageVisibility): StoredImage {
  const taskVisibility = task.visibility || fallbackVisibility || image.visibility || "private";
  const successUpdates = (item: CreationTaskDataItem) => {
    const width = positiveDimension(item.width);
    const height = positiveDimension(item.height);
    return {
      taskId: task.id,
      taskStatus: "success" as const,
      status: "success" as const,
      b64_json: item.b64_json,
      url: item.url,
      path: item.url ? getManagedImagePathFromUrl(item.url) || image.path : image.path,
      visibility: taskVisibility,
      width,
      height,
      resolution: item.resolution || (width && height ? `${width}x${height}` : image.resolution),
      outputFormat: item.output_format || task.output_format || image.outputFormat,
      revised_prompt: item.revised_prompt,
      text_response: undefined,
      error: undefined,
    };
  };
  if (task.status === "success") {
    if (task.output_type === "text") {
      return updateStoredImage(image, {
        taskId: task.id,
        taskStatus: "success",
        status: "message",
        text_response: task.data?.[dataIndex]?.text_response || task.error || "",
        b64_json: undefined,
        url: undefined,
        path: undefined,
        visibility: undefined,
        revised_prompt: undefined,
        error: undefined,
      });
    }
    const item = task.data?.[dataIndex];
    if (!item?.b64_json && !item?.url) {
      if (dataIndex > 0 && image.taskId !== image.id) {
        const slotStatus = creationTaskImageStatus(task, dataIndex);
        if (slotStatus === "error" || slotStatus === "cancelled") {
          return updateStoredImage(image, {
            taskId: task.id,
            taskStatus: slotStatus,
            status: slotStatus === "cancelled" ? "cancelled" : "error",
            error: slotStatus === "cancelled" ? task.error || "任务已终止" : formatCreationTaskErrorMessage(task.error || "生成失败"),
          });
        }
        return updateStoredImage(image, {
          taskId: image.id,
          taskStatus: "queued",
          status: "loading",
          error: undefined,
        });
      }
      return updateStoredImage(image, {
        taskId: task.id,
        taskStatus: "success",
        status: "error",
        error: `未返回第 ${dataIndex + 1} 张图片数据`,
      });
    }
    return updateStoredImage(image, successUpdates(item));
  }

  if (task.status === "queued" || task.status === "running") {
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, successUpdates(item));
    }
    return updateStoredImage(image, {
      taskId: task.id,
      taskStatus: creationTaskImageStatus(task, dataIndex) || (task.status === "queued" ? "queued" : "running"),
      status: "loading",
      text_response: undefined,
      error: undefined,
    });
  }

  if (task.status === "error") {
    if (task.output_type === "text") {
      return updateStoredImage(image, {
        taskId: task.id,
        taskStatus: "success",
        status: "message",
        text_response: task.error || "",
        b64_json: undefined,
        url: undefined,
        path: undefined,
        visibility: undefined,
        revised_prompt: undefined,
        error: undefined,
      });
    }
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, successUpdates(item));
    }
    return updateStoredImage(image, {
      taskId: task.id,
      taskStatus: undefined,
      status: "error",
      text_response: undefined,
      error: formatCreationTaskErrorMessage(task.error || "生成失败"),
    });
  }

  if (task.status === "cancelled") {
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, successUpdates(item));
    }
    return updateStoredImage(image, {
      taskId: task.id,
      taskStatus: undefined,
      status: "cancelled",
      error: task.error || "任务已终止",
    });
  }

  return updateStoredImage(image, {
    taskId: task.id,
    taskStatus: creationTaskImageStatus(task, dataIndex) || "queued",
    status: "loading",
    text_response: undefined,
    error: undefined,
  });
}

function isActiveCreationTask(task: CreationTask) {
  return task.status === "queued" || task.status === "running";
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

function getStoredImageSizeSelection(): ImageSizeSelection {
  if (typeof window === "undefined") {
    return getImageSizeSelectionFromSize("");
  }
  const fallbackSelection = getImageSizeSelectionFromSize(window.localStorage.getItem(IMAGE_SIZE_STORAGE_KEY) || "");
  const storedSizeMode = window.localStorage.getItem(IMAGE_SIZE_MODE_STORAGE_KEY);
  const storedAspectRatio = window.localStorage.getItem(IMAGE_ASPECT_RATIO_STORAGE_KEY) || "";
  const storedResolution = window.localStorage.getItem(IMAGE_RESOLUTION_STORAGE_KEY);
  const customRatio = window.localStorage.getItem(IMAGE_CUSTOM_RATIO_STORAGE_KEY) || fallbackSelection.customRatio;
  const customWidth = window.localStorage.getItem(IMAGE_CUSTOM_WIDTH_STORAGE_KEY) || fallbackSelection.customWidth;
  const customHeight = window.localStorage.getItem(IMAGE_CUSTOM_HEIGHT_STORAGE_KEY) || fallbackSelection.customHeight;
  if (isImageSizeMode(storedSizeMode) && isImageAspectRatio(storedAspectRatio) && isImageResolution(storedResolution)) {
    return {
      mode: storedSizeMode,
      aspectRatio: storedAspectRatio,
      resolution: storedResolution,
      customRatio,
      customWidth,
      customHeight,
    };
  }
  return fallbackSelection;
}

function getStoredImageOutputFormat(): ImageOutputFormat {
  if (typeof window === "undefined") {
    return DEFAULT_IMAGE_OUTPUT_FORMAT;
  }
  const storedFormat = window.localStorage.getItem(IMAGE_OUTPUT_FORMAT_STORAGE_KEY);
  return isImageOutputFormat(storedFormat) ? storedFormat : DEFAULT_IMAGE_OUTPUT_FORMAT;
}

function getStoredImageOutputCompression(): string {
  if (typeof window === "undefined") {
    return "";
  }
  const normalized = normalizeOutputCompressionValue(window.localStorage.getItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY));
  return normalized === undefined ? "" : String(normalized);
}

function serializeImageSizeSelection(selection: ImageSizeSelection): StoredImageSizeSelection {
  return {
    mode: selection.mode,
    aspectRatio: selection.aspectRatio,
    resolution: selection.resolution,
    customRatio: selection.customRatio,
    customWidth: selection.customWidth,
    customHeight: selection.customHeight,
  };
}

function restoreImageSizeSelection(stored: StoredImageSizeSelection | undefined, fallbackSize: string): ImageSizeSelection {
  const fallbackSelection = getImageSizeSelectionFromSize(fallbackSize);
  if (!stored) {
    return fallbackSelection;
  }
  return {
    mode: isImageSizeMode(stored.mode) ? stored.mode : fallbackSelection.mode,
    aspectRatio: isImageAspectRatio(stored.aspectRatio) ? stored.aspectRatio : fallbackSelection.aspectRatio,
    resolution: isImageResolution(stored.resolution) ? stored.resolution : fallbackSelection.resolution,
    customRatio: stored.customRatio || fallbackSelection.customRatio,
    customWidth: stored.customWidth || fallbackSelection.customWidth,
    customHeight: stored.customHeight || fallbackSelection.customHeight,
  };
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

function formatCreationTaskErrorMessage(message: string) {
  const trimmed = String(message || "").trim();
  if (!trimmed) {
    return "生成图片失败";
  }

  const normalized = trimmed.toLowerCase();
  if (normalized.includes("user balance insufficient")) {
    return "用户余额不足";
  }
  if (normalized.includes("user quota exceeded")) {
    return "用户配额不足";
  }
  if (normalized.includes("an error occurred while processing your request")) {
    const requestId = trimmed.match(/request id\s+([a-z0-9-]+)/i)?.[1];
    return [
      "上游处理图片请求失败，可能是提示词内容过多、账号能力限制或当前图片链路繁忙。",
      "建议减少提示词内容，或稍后重试；Codex 结构化高分辨率请求可降低尺寸后再试。",
      requestId ? `请求 ID：${requestId}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  }
  if (normalized.includes("no images generated") && normalized.includes("model may have refused")) {
    return "没有生成图片，模型可能检测到敏感内容并拒绝了这次请求，请调整提示词后重试。";
  }
  if (normalized.includes("timed out waiting for async image generation")) {
    return "图片生成等待超时，建议稍后重试；如果使用 Codex 结构化高分辨率参数，可降低尺寸后再试。";
  }
  if (normalized.includes("no available image quota")) {
    return "当前没有可用的图片额度，请检查账号额度或稍后重试。";
  }

  return trimmed;
}

function formatCreationTaskError(error: unknown, fallback = "生成图片失败") {
  return formatCreationTaskErrorMessage(error instanceof Error ? error.message : String(error || fallback));
}

function formatBillingSummary(session: NonNullable<ReturnType<typeof useAuthGuard>["session"]>) {
  const billing = session.billing;
  if (!billing) {
    return "本地额度 --";
  }
  if (billing.unlimited) {
    return "本地额度无限";
  }
  if (billing.type === "subscription") {
    return `订阅剩余 ${billing.available}`;
  }
  return `余额 ${billing.available}`;
}

function hasEnoughBilling(session: NonNullable<ReturnType<typeof useAuthGuard>["session"]>, estimated: number) {
  const billing = session.billing;
  return !billing || billing.unlimited || Math.max(0, Number(billing.available) || 0) >= estimated;
}

function deriveTurnStatus(turn: ImageTurn): Pick<ImageTurn, "status" | "error"> {
  const loadingCounts = getImageTurnLoadingCounts(turn);
  const failedCount = turn.images.filter((image) => image.status === "error").length;
  const successCount = turn.images.filter((image) => image.status === "success").length;
  const cancelledCount = turn.images.filter((image) => image.status === "cancelled").length;
  const messageCount = turn.images.filter((image) => image.status === "message").length;
  if (loadingCounts.running > 0) {
    return { status: "generating", error: undefined };
  }
  if (loadingCounts.queued > 0) {
    return { status: "queued", error: undefined };
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

function deriveTurnStatusFromTaskMap(turn: ImageTurn, images: StoredImage[]): Pick<ImageTurn, "status" | "error"> {
  return deriveTurnStatus({ ...turn, images });
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

function isMissingRecoverableTaskIdError(error?: string) {
  return error === MISSING_RECOVERABLE_TASK_ID_ERROR;
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

function buildCreationTaskMessages(conversation: ImageConversation, activeTurnId: string): CreationTaskMessage[] {
  const messages: CreationTaskMessage[] = [];
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

async function syncConversationCreationTasks(items: ImageConversation[]) {
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

  let taskList: Awaited<ReturnType<typeof fetchCreationTasks>>;
  try {
    taskList = await fetchCreationTasks(taskIds);
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
        const nextImage = taskDataToStoredImage(image, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility);
        if (nextImage !== image) {
          turnChanged = true;
        }
        return nextImage;
      });
      if (!turnChanged) {
        return turn;
      }
      changed = true;
      const derived = deriveTurnStatusFromTaskMap(turn, images);
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
      const recoveredImages = turn.images.map((image, imageIndex) => {
        if (image.status === "error" && isMissingBatchImageDataError(image.error)) {
          turnChanged = true;
          return {
            ...image,
            taskId: image.id,
            status: "loading" as const,
            error: undefined,
          };
        }
        if (turn.mode === "chat" && image.status === "error" && isMissingRecoverableTaskIdError(image.error)) {
          turnChanged = true;
          return {
            ...image,
            taskId: imageTaskIdForImage(turn.id, turn.images, imageIndex),
            status: "loading" as const,
            error: undefined,
          };
        }
        if (turn.mode === "chat" && image.status === "loading" && !image.taskId) {
          turnChanged = true;
          return {
            ...image,
            taskId: imageTaskIdForImage(turn.id, turn.images, imageIndex),
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
          error: MISSING_RECOVERABLE_TASK_ID_ERROR,
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

  return syncConversationCreationTasks(normalized);
}


function ImagePageContent({ session }: { session: NonNullable<ReturnType<typeof useAuthGuard>["session"]> }) {
  const isSubmitDispatchingRef = useRef(false);
  const retryingImageIdsRef = useRef(new Set<string>());
  const cancelledTurnIdsRef = useRef(new Set<string>());
  const conversationsRef = useRef<ImageConversation[]>([]);
  const resultsViewportRef = useRef<HTMLDivElement>(null);
  const composerDockRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const editFileInputRef = useRef<HTMLInputElement>(null);
  const promptApplyRequestIdRef = useRef(0);

  const [imagePrompt, setImagePrompt] = useState("");
  const [composerMode, setComposerMode] = useState<ComposerMode>(getStoredComposerMode);
  const [imageModel, setImageModel] = useState<ImageModel>(getStoredImageModel);
  const [imageCount, setImageCount] = useState("1");
  const [imageSizeMode, setImageSizeMode] = useState<ImageSizeMode>(() => getStoredImageSizeSelection().mode);
  const [imageAspectRatio, setImageAspectRatio] = useState<ImageAspectRatio>(() => getStoredImageSizeSelection().aspectRatio);
  const [imageResolution, setImageResolution] = useState<ImageResolution>(() => getStoredImageSizeSelection().resolution);
  const [imageCustomRatio, setImageCustomRatio] = useState(() => getStoredImageSizeSelection().customRatio);
  const [imageCustomWidth, setImageCustomWidth] = useState(() => getStoredImageSizeSelection().customWidth);
  const [imageCustomHeight, setImageCustomHeight] = useState(() => getStoredImageSizeSelection().customHeight);
  const [imageOutputFormat, setImageOutputFormat] = useState<ImageOutputFormat>(getStoredImageOutputFormat);
  const [imageOutputCompression, setImageOutputCompression] = useState(getStoredImageOutputCompression);
  const [defaultImageVisibility, setDefaultImageVisibility] = useState<ImageVisibility>("private");
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
  const [progressByTurnKey, setProgressByTurnKey] = useState<Record<string, ImageTurnProgress>>(
    getImageTurnProgressSnapshot,
  );
  const [progressNow, setProgressNow] = useState(Date.now());
  const [composerDockHeight, setComposerDockHeight] = useState(0);
  const [visibilityMutatingImageKey, setVisibilityMutatingImageKey] = useState("");
  const canInspectAccounts = session.role === "admin" || session.apiPermissions.includes("get/api/accounts");

  const parsedCount = useMemo(() => normalizeRequestedImageCount(imageCount), [imageCount]);
  const imageSize = useMemo(
    () => {
      const request = buildEffectiveImageSizeRequest(imageModel, {
        mode: imageSizeMode,
        aspectRatio: imageAspectRatio,
        resolution: imageResolution,
        customRatio: imageCustomRatio,
        customWidth: imageCustomWidth,
        customHeight: imageCustomHeight,
      });
      return request.size;
    },
    [imageAspectRatio, imageCustomHeight, imageCustomRatio, imageCustomWidth, imageModel, imageResolution, imageSizeMode],
  );
  const editingDraftSizeRequest = useMemo(() => {
    if (!editingTurnDraft || editingTurnDraft.mode === "chat") {
      return null;
    }
    return buildEffectiveImageSizeRequest(editingTurnDraft.model, {
      mode: editingTurnDraft.sizeMode,
      aspectRatio: editingTurnDraft.aspectRatio,
      resolution: editingTurnDraft.resolution,
      customRatio: editingTurnDraft.customRatio,
      customWidth: editingTurnDraft.customWidth,
      customHeight: editingTurnDraft.customHeight,
    });
  }, [editingTurnDraft]);
  const editingDraftEffectiveSizeSelection = editingDraftSizeRequest?.selection;
  const editingDraftImageSize = useMemo(() => {
    return editingDraftSizeRequest?.size ?? "";
  }, [editingDraftSizeRequest]);
  const editingDraftStructuredParameters = editingTurnDraft
    ? supportsStructuredImageParameters(editingTurnDraft.model)
    : false;
  const editingDraftOutputControls = editingTurnDraft
    ? supportsImageOutputControls(editingTurnDraft.model)
    : false;
  const editingDraftOfficialRoute = editingTurnDraft
    ? usesOfficialImageRoute(editingTurnDraft.model)
    : false;
  const editingDraftCustomRatioInvalid = editingTurnDraft && editingDraftEffectiveSizeSelection
    ? isInvalidCustomRatioSelection(
        editingDraftEffectiveSizeSelection.mode,
        editingDraftEffectiveSizeSelection.aspectRatio,
        editingDraftEffectiveSizeSelection.customRatio,
      )
    : false;
  const editingDraftSizePreviewLabel =
    editingTurnDraft && editingTurnDraft.mode !== "chat" && editingDraftEffectiveSizeSelection
      ? editingDraftImageSize
        ? formatImageSizeDisplay(editingDraftImageSize)
        : editingDraftEffectiveSizeSelection.mode === "auto" ||
            (editingDraftEffectiveSizeSelection.mode === "ratio" &&
              editingDraftEffectiveSizeSelection.resolution === "auto" &&
              !editingDraftCustomRatioInvalid)
          ? "Auto"
          : "尺寸无效"
      : "";
  const editingDraftSizePreviewDetail =
    editingDraftEffectiveSizeSelection?.mode === "ratio"
      ? editingDraftCustomRatioInvalid
        ? "比例需要填写为宽:高"
        : editingDraftEffectiveSizeSelection.resolution === "auto"
          ? editingDraftImageSize
            ? editingDraftOfficialRoute
              ? `将把 ${editingDraftImageSize} 写入提示词作为构图偏好`
              : `将按 ${editingDraftImageSize} 比例下发`
            : editingDraftOfficialRoute
              ? "不写入固定比例，交给官方链路决定"
              : "Auto 比例将交给模型决定"
          : editingDraftImageSize
            ? editingDraftOfficialRoute
              ? `将把 ${formatImageSizeDisplay(editingDraftImageSize)} 作为提示词构图偏好，实际像素以结果为准`
              : `将下发计算后的 ${formatImageSizeDisplay(editingDraftImageSize)}，${getImageSizeRequirementLabel(editingDraftImageSize)}`
            : "比例需要填写为宽:高"
      : editingDraftEffectiveSizeSelection?.mode === "custom"
        ? editingDraftImageSize
          ? `已按链路限制校准为 ${formatImageSizeDisplay(editingDraftImageSize)}，${getImageSizeRequirementLabel(editingDraftImageSize)}`
          : "宽高需要填写正整数"
        : editingDraftOfficialRoute
          ? "不写入尺寸提示，实际像素由官方返回决定"
          : "不会强制指定尺寸";
  const editingDraftSizeIsHighResolution = Boolean(
    editingDraftStructuredParameters && editingDraftImageSize && isHighResolutionImageSize(editingDraftImageSize),
  );
  const composerModelOptions = composerMode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_CREATION_MODEL_OPTIONS;
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
  const billingSummary = formatBillingSummary(session);
  const estimatedBillingUnits = composerMode === "chat" ? 1 : parsedCount;
  const billingBlocked = !hasEnoughBilling(session, estimatedBillingUnits);
  const deleteConfirmTitle = deleteConfirm?.type === "all" ? "清空历史记录" : deleteConfirm?.type === "one" ? "删除对话" : "";
  const deleteConfirmDescription =
    deleteConfirm?.type === "all"
      ? "确认删除全部图片历史记录吗？删除后无法恢复。"
      : deleteConfirm?.type === "one"
        ? "确认删除这条图片对话吗？删除后无法恢复。"
        : "";
  const highResolutionHint = useMemo(
    () => formatHighResolutionHint(canInspectAccounts),
    [canInspectAccounts],
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
    let cancelled = false;

    const refreshConversations = async () => {
      try {
        const items = await listImageConversations();
        if (cancelled) {
          return;
        }
        conversationsRef.current = items;
        setConversations(items);
      } catch {
        // Background updates should not surface noisy toasts while the user is on another workflow.
      }
    };

    const handleConversationsChanged = () => {
      void refreshConversations();
    };

    window.addEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationsChanged);
    return () => {
      cancelled = true;
      window.removeEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationsChanged);
    };
  }, []);

  useEffect(
    () =>
      subscribeImageTurnProgress(() => {
        setProgressByTurnKey(getImageTurnProgressSnapshot());
      }),
    [],
  );

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
        const storedSelection = getStoredImageSizeSelection();
        setImageSizeMode(storedSelection.mode);
        setImageAspectRatio(storedSelection.aspectRatio);
        setImageResolution(storedSelection.resolution);
        setImageCustomRatio(storedSelection.customRatio);
        setImageCustomWidth(storedSelection.customWidth);
        setImageCustomHeight(storedSelection.customHeight);
        setImageOutputFormat(getStoredImageOutputFormat());
        setImageOutputCompression(getStoredImageOutputCompression());

        const items = await listImageConversations();
        const normalizedItems = await recoverConversationHistory(items);
        if (cancelled) {
          return;
        }

        conversationsRef.current = normalizedItems;
        setConversations(normalizedItems);
        const storedConversationId =
          typeof window !== "undefined" ? window.localStorage.getItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY) : null;
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
      window.localStorage.setItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY, selectedConversationId);
    } else {
      window.localStorage.removeItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY);
    }
  }, [selectedConversationId]);

  useEffect(() => {
    const handleOpenConversation = (event: Event) => {
      const conversationId = (event as CustomEvent<{ conversationId?: string }>).detail?.conversationId;
      if (conversationId) {
        setSelectedConversationId(conversationId);
      }
    };

    window.addEventListener(IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT, handleOpenConversation);
    return () => {
      window.removeEventListener(IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT, handleOpenConversation);
    };
  }, []);

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
      return;
    }

    if (!isImageCreationModel(imageModel)) {
      setImageModel(DEFAULT_IMAGE_MODEL);
    }
  }, [composerMode, imageModel]);

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

    window.localStorage.setItem(IMAGE_SIZE_MODE_STORAGE_KEY, imageSizeMode);
    if (imageAspectRatio) {
      window.localStorage.setItem(IMAGE_ASPECT_RATIO_STORAGE_KEY, imageAspectRatio);
    } else {
      window.localStorage.removeItem(IMAGE_ASPECT_RATIO_STORAGE_KEY);
    }
    window.localStorage.setItem(IMAGE_RESOLUTION_STORAGE_KEY, imageResolution);
    window.localStorage.setItem(IMAGE_CUSTOM_RATIO_STORAGE_KEY, imageCustomRatio);
    window.localStorage.setItem(IMAGE_CUSTOM_WIDTH_STORAGE_KEY, imageCustomWidth);
    window.localStorage.setItem(IMAGE_CUSTOM_HEIGHT_STORAGE_KEY, imageCustomHeight);
    if (imageSize) {
      window.localStorage.setItem(IMAGE_SIZE_STORAGE_KEY, imageSize);
      return;
    }
    window.localStorage.removeItem(IMAGE_SIZE_STORAGE_KEY);
  }, [imageAspectRatio, imageCustomHeight, imageCustomRatio, imageCustomWidth, imageResolution, imageSize, imageSizeMode]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    window.localStorage.setItem(IMAGE_OUTPUT_FORMAT_STORAGE_KEY, imageOutputFormat);
    const normalizedCompression = normalizeOutputCompressionValue(imageOutputCompression);
    if (normalizedCompression === undefined || !supportsImageOutputCompression(imageOutputFormat)) {
      window.localStorage.removeItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY, String(normalizedCompression));
  }, [imageOutputCompression, imageOutputFormat]);

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
      setImageTurnProgress(conversationId, turnId, updates);
    },
    [],
  );

  const clearTurnProgress = useCallback((conversationId: string, turnId: string) => {
    clearImageTurnProgress(conversationId, turnId);
  }, []);

  const clearComposerInputs = useCallback(() => {
    promptApplyRequestIdRef.current += 1;
    setImagePrompt("");
    setImageCount("1");
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
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
      promptApplyRequestIdRef.current += 1;
      setDefaultImageVisibility("private");
    }
  }, []);

  const handleCreateDraft = () => {
    setSelectedConversationId(null);
    resetComposer();
    textareaRef.current?.focus();
  };

  const handleApplyPromptPreset = useCallback(async (preset: ImagePromptPreset) => {
    const requestId = promptApplyRequestIdRef.current + 1;
    promptApplyRequestIdRef.current = requestId;
    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(preset.prompt);
    setImageCount(String(preset.count));
    const presetSizeSelection = getImageSizeSelectionFromSize(preset.size);
    setImageSizeMode(presetSizeSelection.mode);
    setImageAspectRatio(presetSizeSelection.aspectRatio);
    setImageResolution(presetSizeSelection.resolution);
    setImageCustomRatio(presetSizeSelection.customRatio);
    setImageCustomWidth(presetSizeSelection.customWidth);
    setImageCustomHeight(presetSizeSelection.customHeight);
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
    setReferenceImages([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();

    const toastId = toast.loading("正在读取参考图");
    try {
      const referenceImage = await buildReferenceImageFromUrl(preset.imageSrc, 0, "preset-reference");
      if (promptApplyRequestIdRef.current !== requestId) {
        toast.dismiss(toastId);
        return;
      }
      setReferenceImages([referenceImage]);
      toast.dismiss(toastId);
      toast.success("已套用提示词和参考图");
    } catch {
      if (promptApplyRequestIdRef.current !== requestId) {
        toast.dismiss(toastId);
        return;
      }
      toast.dismiss(toastId);
      toast.error("已套用提示词，但参考图读取失败");
    }
  }, []);

  const handleApplyMarketPrompt = useCallback(async (prompt: BananaPrompt) => {
    const referenceImageUrls = getPromptReferenceImageUrls(prompt);
    const requestId = promptApplyRequestIdRef.current + 1;
    promptApplyRequestIdRef.current = requestId;

    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(prompt.prompt);
    setImageCount("1");
    setImageSizeMode("auto");
    setImageAspectRatio("");
    setImageResolution("auto");
    setImageCustomRatio(DEFAULT_IMAGE_CUSTOM_RATIO);
    setImageCustomWidth(DEFAULT_IMAGE_CUSTOM_WIDTH);
    setImageCustomHeight(DEFAULT_IMAGE_CUSTOM_HEIGHT);
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
    setReferenceImages([]);
    setIsPromptMarketOpen(false);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();

    if (referenceImageUrls.length === 0) {
      toast.success("已套用提示词");
      return;
    }

    const toastId = toast.loading(`正在读取 ${referenceImageUrls.length} 张参考图`);
    const results = await Promise.allSettled(
      referenceImageUrls.map((url, index) => buildReferenceImageFromUrl(url, index, "prompt-reference")),
    );
    const loadedReferences = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));

    toast.dismiss(toastId);
    if (promptApplyRequestIdRef.current !== requestId) {
      return;
    }
    if (loadedReferences.length > 0) {
      setReferenceImages(loadedReferences);
    }
    if (loadedReferences.length === referenceImageUrls.length) {
      toast.success("已套用提示词和参考图");
    } else if (loadedReferences.length > 0) {
      toast.error(`已套用提示词，${referenceImageUrls.length - loadedReferences.length} 张参考图读取失败`);
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
    promptApplyRequestIdRef.current += 1;

    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );

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
            : await buildReferenceImageFromStoredImage(
                image,
                `conversation-${conversationId}-${Date.now()}.${imageFileExtensionForOutputFormat(image.outputFormat)}`,
              );
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

  const handleImageVisibilityChange = useCallback(
    async (conversationId: string, turnId: string, imageIndex: number, visibility: ImageVisibility) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      const targetImage = targetTurn?.images[imageIndex];
      if (!targetConversation || !targetTurn || !targetImage) {
        toast.error("未找到对应的图片记录");
        return;
      }
      if (targetImage.status !== "success") {
        toast.error("图片生成成功后才能修改公开状态");
        return;
      }
      const path = targetImage.path || (targetImage.url ? getManagedImagePathFromUrl(targetImage.url) : "");
      if (!path) {
        toast.error("未找到可同步到图库的图片路径");
        return;
      }

      const mutatingKey = `${conversationId}:${turnId}:${targetImage.id}`;
      if (visibilityMutatingImageKey === mutatingKey) {
        return;
      }
      if (visibilityMutatingImageKey) {
        return;
      }
      setVisibilityMutatingImageKey(mutatingKey);
      try {
        const data = await updateManagedImageVisibility(path, visibility);
        const updatedVisibility = data.item.visibility || visibility;
        const updatedPath = data.item.path || path;
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? targetConversation;
          return {
            ...conversation,
            updatedAt: new Date().toISOString(),
            turns: conversation.turns.map((turn) =>
              turn.id === turnId
                ? {
                    ...turn,
                    images: turn.images.map((image, index) =>
                      index === imageIndex
                        ? {
                            ...image,
                            path: updatedPath,
                            visibility: updatedVisibility,
                          }
                        : image,
                    ),
                  }
                : turn,
            ),
          };
        });
        clearImageManagerCache();
        toast.success(updatedVisibility === "public" ? "已公开到公开图库" : "已取消公开");
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "更新公开状态失败");
      } finally {
        setVisibilityMutatingImageKey("");
      }
    },
    [updateConversation, visibilityMutatingImageKey],
  );

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
    const sizeSelection = restoreImageSizeSelection(targetTurn.sizeSelection, targetTurn.size);
    setEditingTurnDraft({
      conversationId,
      turnId,
      prompt: targetTurn.prompt,
      model:
        targetTurn.mode === "chat"
          ? isChatModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_CHAT_MODEL
          : isImageCreationModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_IMAGE_MODEL,
      mode: targetTurn.mode,
      count: targetTurn.mode === "chat" ? "1" : String(normalizeRequestedImageCount(targetTurn.count || targetTurn.images.length || 1)),
      sizeMode: targetTurn.mode === "chat" ? "auto" : sizeSelection.mode,
      aspectRatio: targetTurn.mode === "chat" ? "" : sizeSelection.aspectRatio,
      resolution: targetTurn.mode === "chat" ? "auto" : sizeSelection.resolution,
      customRatio: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_RATIO : sizeSelection.customRatio,
      customWidth: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_WIDTH : sizeSelection.customWidth,
      customHeight: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_HEIGHT : sizeSelection.customHeight,
      outputFormat: targetTurn.outputFormat || DEFAULT_IMAGE_OUTPUT_FORMAT,
      outputCompression:
        targetTurn.outputCompression === undefined || targetTurn.outputCompression === null
          ? ""
          : String(targetTurn.outputCompression),
      visibility: targetTurn.visibility || "private",
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
      const activeTurnStartedAt = imageTurnStartedAtTimestamp(activeTurn.processingStartedAt, activeTurn.createdAt);
      updateTurnProgress(conversationId, activeTurn.id, {
        message: activeTurn.mode === "chat" ? "正在准备对话请求" : "正在准备生成任务",
        detail:
          activeTurn.mode === "chat"
            ? "正在整理上下文"
            : `准备处理 ${activeTurn.images.filter((image) => image.status === "loading").length || activeTurn.count} 张图片`,
        startedAt: activeTurnStartedAt,
      });
      const applyTasks = async (tasks: CreationTask[]) => {
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
              return task ? taskDataToStoredImage(taskImage, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility) : image;
            });
            const derived = deriveTurnStatusFromTaskMap(turn, images);
            const currentCounts = getImageTurnLoadingCounts(turn);
            const nextCounts = getImageTurnLoadingCounts({ images });
            const nextTurn = {
              ...turn,
              ...derived,
              processingStartedAt:
                nextCounts.running > 0 && currentCounts.running === 0
                  ? new Date().toISOString()
                  : turn.processingStartedAt,
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
                            taskId: imageTaskIdForImage(turn.id, turn.images, imageIndex),
                          }
                        : image,
                    ),
                  }
                : turn,
            ),
          };
        });

        updateTurnProgress(conversationId, activeTurn.id, {
          message:
            activeTurn.mode === "chat" ? "正在准备对话请求" : usesReferenceImages(activeTurn.mode) ? "正在整理参考图" : "正在准备生成请求",
          detail:
            activeTurn.mode === "chat"
              ? "正在整理上下文并创建后台任务"
              : usesReferenceImages(activeTurn.mode)
                ? "正在读取参考图并准备上传"
                : "正在创建图片生成任务",
        });
        const referenceFiles = activeTurn.referenceImages.map((image, index) =>
          dataUrlToFile(image.dataUrl, image.name || `${activeTurn.id}-${index + 1}.png`, image.type),
        );
        if (usesReferenceImages(activeTurn.mode) && referenceFiles.length === 0) {
          throw new Error("未找到可用的参考图");
        }
        const taskMessages = buildCreationTaskMessages(snapshot, activeTurn.id);
        const activeTurnSizeRequest =
          activeTurn.mode === "chat"
            ? { selection: undefined, size: "" }
            : buildEffectiveImageSizeRequest(
                activeTurn.model,
                restoreImageSizeSelection(activeTurn.sizeSelection, activeTurn.size),
              );
        const taskOutputFormat = imageOutputFormatForModel(
          activeTurn.model,
          activeTurn.outputFormat || DEFAULT_IMAGE_OUTPUT_FORMAT,
        );
        const taskOutputCompression =
          taskOutputFormat === undefined
            ? undefined
            : imageOutputCompressionForModel(activeTurn.model, taskOutputFormat, activeTurn.outputCompression);
        const taskImageResolution =
          supportsStructuredImageParameters(activeTurn.model) && activeTurnSizeRequest.selection?.resolution !== "auto"
            ? activeTurnSizeRequest.selection?.resolution
            : undefined;
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
        const submitTaskGroup = (group: { taskId: string; count: number }) => {
          if (activeTurn.mode === "chat") {
            if (activeTurn.referenceImages.length > 0) {
              return createChatCompletionTask(
                group.taskId,
                activeTurn.prompt,
                activeTurn.model,
                taskMessages,
                activeTurn.referenceImages.map((img) => ({ name: img.name, dataUrl: img.dataUrl })),
              );
            }
            return createChatCompletionTask(group.taskId, activeTurn.prompt, activeTurn.model, taskMessages);
          }
          if (usesReferenceImages(activeTurn.mode)) {
            return createImageEditTask(
              group.taskId,
              referenceFiles,
              activeTurn.prompt,
              activeTurn.model,
              activeTurnSizeRequest.size,
              undefined,
              group.count,
              taskMessages,
              activeTurn.visibility || "private",
              taskImageResolution,
              taskOutputFormat,
              taskOutputCompression,
            );
          }
          return createImageGenerationTask(
            group.taskId,
            activeTurn.prompt,
            activeTurn.model,
            activeTurnSizeRequest.size,
            undefined,
            group.count,
            taskMessages,
            activeTurn.visibility || "private",
            taskImageResolution,
            taskOutputFormat,
            taskOutputCompression,
          );
        };
        updateTurnProgress(conversationId, activeTurn.id, {
          message: activeTurn.mode === "chat" ? "正在提交对话请求" : "正在提交生成请求",
          detail: activeTurn.mode === "chat" ? "对话任务正在入队" : `${pendingTaskGroups.length} 个图片任务正在入队`,
        });
        const submitted = await Promise.all(pendingTaskGroups.map(submitTaskGroup));
        let activeTaskIds = new Set(submitted.filter(isActiveCreationTask).map((task) => task.id));
        await applyTasks(submitted);
        const submittedStatus =
          submitted.length > 0 && submitted.every((task) => task.status === "queued") ? "queued" : "generating";
        updateTurnProgress(conversationId, activeTurn.id, imageTaskProgressMessage({ ...activeTurn, status: submittedStatus }));

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
          const pollingTaskIds = Array.from(new Set([...loadingTaskIds, ...activeTaskIds]));
          if (pollingTaskIds.length === 0) {
            break;
          }

          const progressSnapshot = getImageTurnProgressSnapshot()[activeTurnKey];
          const elapsedSeconds =
            progressSnapshot && Number.isFinite(progressSnapshot.startedAt)
              ? Math.max(0, Math.floor((Date.now() - progressSnapshot.startedAt) / 1000))
              : Math.max(0, Math.floor((Date.now() - activeTurnStartedAt) / 1000));
          const progressTurn = latestTurn ?? activeTurn;
          const progressCopy = imageTaskProgressMessage(progressTurn, elapsedSeconds);
          updateTurnProgress(conversationId, activeTurn.id, {
            message: progressCopy.message,
            detail: imageTaskLoadingDetail(progressTurn, progressCopy.detail),
          });
          await sleep(2000);
          const taskList = await fetchCreationTasks(pollingTaskIds);
          activeTaskIds = new Set(taskList.items.filter(isActiveCreationTask).map((task) => task.id));
          if (taskList.items.length > 0) {
            await applyTasks(taskList.items);
          }
          if (taskList.missing_ids.length > 0 && latestTurn) {
            updateTurnProgress(conversationId, activeTurn.id, {
              message: activeTurn.mode === "chat" ? "正在恢复对话任务" : "正在恢复生成任务",
              detail: `${taskList.missing_ids.length} 个任务状态丢失，正在重新提交`,
            });
            const missingTaskGroups = taskList.missing_ids.flatMap((taskId) => {
              const count = latestTurn.images.filter((image) => image.status === "loading" && image.taskId === taskId).length;
              return count > 0 ? [{ taskId, count }] : [];
            });
            const resubmitted = await Promise.all(missingTaskGroups.map(submitTaskGroup));
            if (resubmitted.length > 0) {
              await applyTasks(resubmitted);
            }
          }
        }

        updateTurnProgress(conversationId, activeTurn.id, {
          message: activeTurn.mode === "chat" ? "回复完成" : "生成完成",
          detail: "正在刷新会话",
        });
        if (activeTurn.mode !== "chat") {
          window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
        }
        if (session.role === "user") {
          const data = await fetchProfile();
          await setVerifiedAuthSession(authSessionFromLoginResponse(data, session.key));
        }
      } catch (error) {
        const message = formatCreationTaskError(error, activeTurn.mode === "chat" ? "对话请求失败" : "生成图片失败");
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
    [clearTurnProgress, session.key, session.role, updateConversation, updateTurnProgress],
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

      const results = await Promise.allSettled(taskIds.map((taskId) => cancelCreationTask(taskId)));
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
                return taskDataToStoredImage({ ...image, taskId }, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility);
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
              const images: StoredImage[] = turn.images.map((image, index) =>
                index === imageIndex
                  ? {
                      ...image,
                      taskId: retryTaskId,
                      taskStatus: "queued" as const,
                      status: "loading" as const,
                      b64_json: undefined,
                      url: undefined,
                      path: undefined,
                      width: undefined,
                      height: undefined,
                      resolution: undefined,
                      visibility: targetTurn.mode === "chat" ? undefined : targetTurn.visibility || "private",
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
                processingStartedAt: undefined,
                images,
              };
            }),
          };
        });
        void runConversationQueue(conversationId);
        toast.success("已加入重试队列");
      } catch (error) {
        toast.error(formatCreationTaskError(error, "提交重试失败"));
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
            const visibility = turn.mode === "chat" ? undefined : turn.visibility || "private";
            return {
              ...turn,
              count: imageCount,
              status: "queued",
              error: undefined,
              processingStartedAt: undefined,
              images: Array.from({ length: imageCount }, (_, index): StoredImage => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  taskStatus: "queued" as const,
                  status: "loading" as const,
                  visibility,
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
      const rawDraftSizeSelection = {
        mode: draft.sizeMode,
        aspectRatio: draft.aspectRatio,
        resolution: draft.resolution,
        customRatio: draft.customRatio,
        customWidth: draft.customWidth,
        customHeight: draft.customHeight,
      };
      const draftSizeRequest =
        mode === "chat"
          ? null
          : buildEffectiveImageSizeRequest(draft.model, rawDraftSizeSelection);
      if (
        mode !== "chat" &&
        draftSizeRequest &&
        isInvalidCustomRatioSelection(
          draftSizeRequest.selection.mode,
          draftSizeRequest.selection.aspectRatio,
          draftSizeRequest.selection.customRatio,
        )
      ) {
        toast.error("请输入有效的自定义比例，例如 5:4 或 2.39:1");
        return;
      }
      const draftImageSize = draftSizeRequest?.size ?? "";
      const draftStoredSizeSelection = draftSizeRequest ? serializeImageSizeSelection(draftSizeRequest.selection) : undefined;
      if (
        mode !== "chat" &&
        draftSizeRequest?.selection.mode === "custom" &&
        !draftImageSize
      ) {
        toast.error("请填写有效的宽度和高度");
        return;
      }
      const draftOutputFormat =
        mode === "chat" ? undefined : imageOutputFormatForModel(draft.model, draft.outputFormat);
      const draftOutputCompression =
        draftOutputFormat === undefined
          ? undefined
          : imageOutputCompressionForModel(draft.model, draftOutputFormat, draft.outputCompression);
      if (mode !== "chat" && supportsStructuredImageParameters(draft.model) && isHighResolutionImageSize(draftImageSize)) {
        const sizeLabel = formatImageSizeDisplay(draftImageSize);
        if (regenerate) {
          toast.message(`${sizeLabel} 属于 Codex 结构化高分辨率任务，会直接提交给上游判断。`);
        }
      }
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
              size: draftImageSize,
              sizeSelection: mode === "chat" ? undefined : draftStoredSizeSelection,
              quality: undefined,
              outputFormat: draftOutputFormat,
              outputCompression: draftOutputCompression,
              visibility: mode === "chat" ? "private" : draft.visibility,
            };
            if (!regenerate) {
              return baseTurn;
            }
            return {
              ...baseTurn,
              status: "queued" as const,
              error: undefined,
              processingStartedAt: undefined,
              images: Array.from({ length: imageCount }, (_, index): StoredImage => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  taskStatus: "queued" as const,
                  status: "loading" as const,
                  visibility: baseTurn.mode === "chat" ? undefined : baseTurn.visibility,
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
    const estimatedUnits = composerMode === "chat" ? 1 : parsedCount;
    if (!hasEnoughBilling(session, estimatedUnits)) {
      toast.error(session.billing?.type === "subscription" ? "用户配额不足" : "用户余额不足");
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
          : isImageCreationModel(imageModel)
            ? imageModel
            : DEFAULT_IMAGE_MODEL;
      const requestedCount = effectiveImageMode === "chat" ? 1 : parsedCount;
      const rawImageSizeSelection = {
        mode: imageSizeMode,
        aspectRatio: imageAspectRatio,
        resolution: imageResolution,
        customRatio: imageCustomRatio,
        customWidth: imageCustomWidth,
        customHeight: imageCustomHeight,
      };
      const currentImageSizeRequest =
        effectiveImageMode === "chat"
          ? null
          : buildEffectiveImageSizeRequest(effectiveModel, rawImageSizeSelection);
      if (
        effectiveImageMode !== "chat" &&
        currentImageSizeRequest?.selection.mode === "custom" &&
        !currentImageSizeRequest.size
      ) {
        toast.error("请填写有效的宽度和高度");
        return;
      }
      if (
        effectiveImageMode !== "chat" &&
        currentImageSizeRequest &&
        isInvalidCustomRatioSelection(
          currentImageSizeRequest.selection.mode,
          currentImageSizeRequest.selection.aspectRatio,
          currentImageSizeRequest.selection.customRatio,
        )
      ) {
        toast.error("请输入有效的自定义比例，例如 5:4 或 2.39:1");
        return;
      }
      const currentImageSize = currentImageSizeRequest?.size ?? "";
      const currentImageSizeSelection = currentImageSizeRequest
        ? serializeImageSizeSelection(currentImageSizeRequest.selection)
        : undefined;
      const effectiveOutputFormat =
        effectiveImageMode === "chat" ? undefined : imageOutputFormatForModel(effectiveModel, imageOutputFormat);
      const effectiveOutputCompression =
        effectiveOutputFormat === undefined
          ? undefined
          : imageOutputCompressionForModel(effectiveModel, effectiveOutputFormat, imageOutputCompression);
      const isHighResolutionRequest =
        effectiveImageMode !== "chat" &&
        supportsStructuredImageParameters(effectiveModel) &&
        isHighResolutionImageSize(currentImageSize);
      if (isHighResolutionRequest) {
        const sizeLabel = formatImageSizeDisplay(currentImageSize);
        toast.message(`${sizeLabel} 属于 Codex 结构化高分辨率任务，会直接提交给上游判断。`);
      }
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
        referenceImages: effectiveImageMode === "chat" ? referenceImages : usesReferenceImages(effectiveImageMode) ? referenceImages : [],
        count: requestedCount,
        size: effectiveImageMode === "chat" ? "" : currentImageSize,
        sizeSelection: effectiveImageMode === "chat" ? undefined : currentImageSizeSelection,
        quality: undefined,
        outputFormat: effectiveOutputFormat,
        outputCompression: effectiveImageMode === "chat" ? undefined : effectiveOutputCompression,
        visibility: effectiveImageMode === "chat" ? "private" : defaultImageVisibility,
        images: Array.from({ length: requestedCount }, (_, index): StoredImage => {
          const imageId = `${turnId}-${index}`;
          return {
            id: imageId,
            taskId: imageTaskBatchId(turnId, index),
            taskStatus: "queued" as const,
            status: "loading" as const,
            visibility: effectiveImageMode === "chat" ? undefined : defaultImageVisibility,
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
        startedAt: Date.parse(now),
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
      toast.error(formatCreationTaskError(error, "提交任务失败"));
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
                              className="absolute -top-1 -right-1 z-10 inline-flex size-6 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 shadow-sm transition hover:text-stone-900"
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

                  <div className={cn("grid grid-cols-1 gap-3", editingTurnDraft.mode === "chat" ? "sm:grid-cols-1" : "sm:grid-cols-2 lg:grid-cols-4")}>
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
                            {(editingTurnDraft.mode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_CREATION_MODEL_OPTIONS).map((option) => (
                              <SelectItem key={option.value} value={option.value}>
                                {option.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </label>
                    {editingTurnDraft.mode !== "chat" && editingDraftEffectiveSizeSelection ? (
                      <>
                        <div className="rounded-2xl border border-sky-100 bg-sky-50 px-3 py-2 text-xs leading-5 text-sky-900 sm:col-span-2 lg:col-span-4">
                          {editingDraftOfficialRoute
                            ? "官方链路只会把比例写入提示词作为构图偏好，不下发 1080P / 2K / 4K 或质量参数；格式由后端保存结果时处理，压缩率仅适用于 JPEG。"
                            : "Codex 链路会下发结构化尺寸、格式和 JPEG 压缩率参数；后端只保存上游返回的图片，不做格式二次转换。Free 账号会被上游 Codex 图片接口拒绝。"}
                        </div>
                        <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                          {editingDraftOfficialRoute ? "构图" : "尺寸"}
                          <Select
                            value={editingDraftEffectiveSizeSelection.mode}
                            onValueChange={(value) =>
                              setEditingTurnDraft((current) =>
                                current && isImageSizeMode(value) ? { ...current, sizeMode: value } : current,
                              )
                            }
                          >
                            <SelectTrigger>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectGroup>
                                {IMAGE_SIZE_MODE_OPTIONS.filter((option) => editingDraftStructuredParameters || option.value !== "custom").map((option) => (
                                  <SelectItem key={option.value} value={option.value}>
                                    {option.label}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </label>
                        {editingDraftStructuredParameters && editingDraftEffectiveSizeSelection.mode === "custom" ? (
                          <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-end gap-2 lg:col-span-2">
                            <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                              宽度
                              <Input
                                type="number"
                                inputMode="numeric"
                                min="1"
                                step="1"
                                value={editingTurnDraft.customWidth}
                                onChange={(event) =>
                                  setEditingTurnDraft((current) =>
                                    current ? { ...current, customWidth: event.target.value } : current,
                                  )
                                }
                              />
                            </label>
                            <span className="pb-2 text-sm font-medium text-stone-400">x</span>
                            <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                              高度
                              <Input
                                type="number"
                                inputMode="numeric"
                                min="1"
                                step="1"
                                value={editingTurnDraft.customHeight}
                                onChange={(event) =>
                                  setEditingTurnDraft((current) =>
                                    current ? { ...current, customHeight: event.target.value } : current,
                                  )
                                }
                              />
                            </label>
                          </div>
                        ) : null}
                        {editingDraftEffectiveSizeSelection.mode === "ratio" ? (
                          <>
                            <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                              比例
                              <Select
                                value={editingTurnDraft.aspectRatio || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                                onValueChange={(value) =>
                                  setEditingTurnDraft((current) =>
                                    current
                                      ? {
                                          ...current,
                                          aspectRatio:
                                            value === EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE
                                              ? ""
                                              : isImageAspectRatio(value)
                                                ? value
                                                : current.aspectRatio,
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
                                    {IMAGE_ASPECT_RATIO_OPTIONS.map((option) => (
                                      <SelectItem
                                        key={option.label}
                                        value={option.value || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                                      >
                                        {option.label}
                                      </SelectItem>
                                    ))}
                                  </SelectGroup>
                                </SelectContent>
                              </Select>
                            </label>
                            {editingDraftStructuredParameters ? (
                              <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                                分辨率
                                <Select
                                  value={editingTurnDraft.resolution}
                                  onValueChange={(value) =>
                                    setEditingTurnDraft((current) =>
                                      current && isImageResolution(value) ? { ...current, resolution: value } : current,
                                    )
                                  }
                                >
                                  <SelectTrigger>
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    <SelectGroup>
                                      {IMAGE_RESOLUTION_OPTIONS.map((option) => (
                                        <SelectItem key={option.value} value={option.value}>
                                          {option.label}
                                        </SelectItem>
                                      ))}
                                    </SelectGroup>
                                  </SelectContent>
                                </Select>
                              </label>
                            ) : null}
                            {editingTurnDraft.aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO ? (
                              <label className="flex flex-col gap-2 text-sm font-medium text-stone-700 sm:col-span-2">
                                自定义比例
                                <Input
                                  value={editingTurnDraft.customRatio}
                                  onChange={(event) =>
                                    setEditingTurnDraft((current) =>
                                      current ? { ...current, customRatio: event.target.value } : current,
                                    )
                                  }
                                  placeholder="例如 5:4 / 2.39:1"
                                  aria-invalid={editingDraftCustomRatioInvalid}
                                  className={cn(editingDraftCustomRatioInvalid && "border-red-300 focus-visible:ring-red-500/20")}
                                />
                              </label>
                            ) : null}
                          </>
                        ) : null}
                        {editingDraftOutputControls ? (
                          <>
                            <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                              格式
                              <Select
                                value={editingTurnDraft.outputFormat}
                                onValueChange={(value) =>
                                  setEditingTurnDraft((current) =>
                                    current && isImageOutputFormat(value)
                                      ? {
                                          ...current,
                                          outputFormat: value,
                                          outputCompression: supportsImageOutputCompression(value) ? current.outputCompression : "",
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
                                    {IMAGE_OUTPUT_FORMAT_OPTIONS.map((option) => (
                                      <SelectItem key={option.value} value={option.value}>
                                        {option.label}
                                      </SelectItem>
                                    ))}
                                  </SelectGroup>
                                </SelectContent>
                              </Select>
                            </label>
                            <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                              压缩率
                              <Input
                                type="number"
                                inputMode="numeric"
                                min="0"
                                max="100"
                                step="1"
                                value={editingTurnDraft.outputCompression}
                                disabled={!supportsImageOutputCompression(editingTurnDraft.outputFormat)}
                                onChange={(event) =>
                                  setEditingTurnDraft((current) =>
                                    current ? { ...current, outputCompression: event.target.value } : current,
                                  )
                                }
                                placeholder={supportsImageOutputCompression(editingTurnDraft.outputFormat) ? "0-100" : "仅 JPEG"}
                              />
                            </label>
                          </>
                        ) : null}
                        {editingDraftEffectiveSizeSelection.mode !== "auto" ? (
                          <>
                            <div className="rounded-2xl border border-stone-200 bg-stone-50 px-3 py-2 text-sm sm:col-span-2 lg:col-span-4">
                              <div className="flex min-w-0 items-center justify-between gap-3">
                                <span className="shrink-0 font-medium text-stone-600">
                                  {editingDraftOfficialRoute ? "构图偏好" : "计算后分辨率"}
                                </span>
                                <span className={cn(
                                  "min-w-0 truncate text-right font-mono font-semibold",
                                  editingDraftSizeIsHighResolution ? "text-amber-700" : "text-stone-900",
                                )}>
                                  {editingDraftSizePreviewLabel}
                                </span>
                              </div>
                              <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-stone-500">
                                <span className="min-w-0 truncate">{editingDraftSizePreviewDetail}</span>
                                {editingDraftSizeIsHighResolution ? (
                                  <span className="shrink-0 rounded-full bg-amber-50 px-2 py-0.5 text-[11px] font-medium text-amber-700 ring-1 ring-amber-100">
                                    高分辨率
                                  </span>
                                ) : null}
                              </div>
                            </div>
                            {editingDraftSizeIsHighResolution ? (
                              <div className="rounded-2xl border border-amber-100 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 sm:col-span-2 lg:col-span-4">
                                {highResolutionHint}
                              </div>
                            ) : null}
                          </>
                        ) : null}
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
              onImageVisibilityChange={handleImageVisibilityChange}
              visibilityMutatingImageKey={visibilityMutatingImageKey}
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
                imageSizeMode={imageSizeMode}
                imageAspectRatio={imageAspectRatio}
                imageResolution={imageResolution}
                imageCustomRatio={imageCustomRatio}
                imageCustomWidth={imageCustomWidth}
                imageCustomHeight={imageCustomHeight}
                imageOutputFormat={imageOutputFormat}
                imageOutputCompression={imageOutputCompression}
                highResolutionHint={highResolutionHint}
                billingSummary={billingSummary}
                estimatedBillingUnits={estimatedBillingUnits}
                billingBlocked={billingBlocked}
                referenceImages={referenceImages}
                textareaRef={textareaRef}
                fileInputRef={fileInputRef}
                onComposerModeChange={handleComposerModeChange}
                onPromptChange={setImagePrompt}
                onImageCountChange={setImageCount}
                onImageModelChange={setImageModel}
                onImageSizeModeChange={setImageSizeMode}
                onImageAspectRatioChange={setImageAspectRatio}
                onImageResolutionChange={setImageResolution}
                onImageCustomRatioChange={setImageCustomRatio}
                onImageCustomWidthChange={setImageCustomWidth}
                onImageCustomHeightChange={setImageCustomHeight}
                onImageOutputFormatChange={setImageOutputFormat}
                onImageOutputCompressionChange={setImageOutputCompression}
                onSubmit={handleSubmit}
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
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/image");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return <ImagePageContent session={session} />;
}
