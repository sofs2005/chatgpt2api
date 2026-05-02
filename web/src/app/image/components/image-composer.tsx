"use client";
import {
  ArrowUp,
  Bot,
  Check,
  ChevronDown,
  CircleHelp,
  Image as ImageIcon,
  ImagePlus,
  MessageCircle,
  Plus,
  SlidersHorizontal,
  Store,
  X,
} from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
  type RefObject,
} from "react";

import { ImageLightbox } from "@/components/image-lightbox";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import {
  IMAGE_ASPECT_RATIO_OPTIONS,
  IMAGE_RESOLUTION_OPTIONS,
  type ImageAspectRatio,
  type ImageResolution,
} from "@/app/image/image-options";
import type { ImageModel, ImageQuality } from "@/lib/api";
import { cn } from "@/lib/utils";

type ImageComposerProps = {
  composerMode: "chat" | "image";
  prompt: string;
  imageCount: string;
  imageModel: ImageModel;
  imageModelOptions: ReadonlyArray<{ value: ImageModel; label: string }>;
  imageAspectRatio: ImageAspectRatio;
  imageResolution: ImageResolution;
  imageQuality: ImageQuality;
  imageQualityOptions: ReadonlyArray<{ value: ImageQuality; label: string; description: string }>;
  imageOutputHint: ReactNode;
  referenceImages: Array<{ name: string; dataUrl: string }>;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  fileInputRef: RefObject<HTMLInputElement | null>;
  onComposerModeChange: (mode: "chat" | "image") => void;
  onPromptChange: (value: string) => void;
  onImageCountChange: (value: string) => void;
  onImageModelChange: (value: ImageModel) => void;
  onImageAspectRatioChange: (value: ImageAspectRatio) => void;
  onImageResolutionChange: (value: ImageResolution) => void;
  onImageQualityChange: (value: ImageQuality) => void;
  onSubmit: () => void | Promise<void>;
  onOpenPromptMarket: () => void;
  onReferenceImageChange: (files: File[]) => void | Promise<void>;
  onRemoveReferenceImage: (index: number) => void;
};

const PROMPT_AREA_MIN_HEIGHT = 74;
const PROMPT_AREA_DEFAULT_HEIGHT = 104;
const PROMPT_AREA_MAX_HEIGHT = 320;
const PROMPT_AREA_KEYBOARD_STEP = 16;
const IMAGE_FILE_EXTENSION_PATTERN = /\.(avif|bmp|gif|heic|heif|jpeg|jpg|png|svg|webp)$/i;

function getPromptAreaMaxHeight() {
  if (typeof window === "undefined") {
    return PROMPT_AREA_MAX_HEIGHT;
  }
  return Math.max(PROMPT_AREA_MIN_HEIGHT, Math.min(PROMPT_AREA_MAX_HEIGHT, Math.floor(window.innerHeight * 0.42)));
}

function clampPromptAreaHeight(height: number) {
  return Math.min(Math.max(height, PROMPT_AREA_MIN_HEIGHT), getPromptAreaMaxHeight());
}

function isImageFile(file: File) {
  return file.type.startsWith("image/") || IMAGE_FILE_EXTENSION_PATTERN.test(file.name);
}

function getImageFiles(files: FileList | File[]) {
  return Array.from(files).filter(isImageFile);
}

function hasDraggedFiles(dataTransfer: DataTransfer) {
  return Array.from(dataTransfer.types).includes("Files");
}

function hasDraggedImage(dataTransfer: DataTransfer) {
  if (!hasDraggedFiles(dataTransfer)) {
    return false;
  }

  const items = Array.from(dataTransfer.items);
  if (items.length === 0) {
    return true;
  }

  return items.some((item) => item.kind === "file" && (item.type === "" || item.type.startsWith("image/")));
}

function ImageComposerDock({ children }: { children: ReactNode }) {
  return (
    <div className="w-full">{children}</div>
  );
}

export function ImageComposer({
  composerMode,
  prompt,
  imageCount,
  imageModel,
  imageModelOptions,
  imageAspectRatio,
  imageResolution,
  imageQuality,
  imageQualityOptions,
  imageOutputHint,
  referenceImages,
  textareaRef,
  fileInputRef,
  onComposerModeChange,
  onPromptChange,
  onImageCountChange,
  onImageModelChange,
  onImageAspectRatioChange,
  onImageResolutionChange,
  onImageQualityChange,
  onSubmit,
  onOpenPromptMarket,
  onReferenceImageChange,
  onRemoveReferenceImage,
}: ImageComposerProps) {
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [isModelMenuOpen, setIsModelMenuOpen] = useState(false);
  const [isAspectRatioMenuOpen, setIsAspectRatioMenuOpen] = useState(false);
  const [isResolutionMenuOpen, setIsResolutionMenuOpen] = useState(false);
  const [isQualityMenuOpen, setIsQualityMenuOpen] = useState(false);
  const [isOutputHintOpen, setIsOutputHintOpen] = useState(false);
  const [isImageSettingsOpen, setIsImageSettingsOpen] = useState(false);
  const [promptAreaHeight, setPromptAreaHeight] = useState(PROMPT_AREA_DEFAULT_HEIGHT);
  const [isPromptAreaResizing, setIsPromptAreaResizing] = useState(false);
  const [isReferenceImageDragActive, setIsReferenceImageDragActive] = useState(false);
  const composerPanelRef = useRef<HTMLDivElement>(null);
  const composerToolbarRef = useRef<HTMLDivElement>(null);
  const modelMenuRef = useRef<HTMLDivElement>(null);
  const aspectRatioMenuRef = useRef<HTMLDivElement>(null);
  const resolutionMenuRef = useRef<HTMLDivElement>(null);
  const qualityMenuRef = useRef<HTMLDivElement>(null);
  const promptAreaResizeRef = useRef<{ pointerOffsetY: number } | null>(null);
  const referenceImageDragDepthRef = useRef(0);
  const lightboxImages = useMemo(
    () => referenceImages.map((image, index) => ({ id: `${image.name}-${index}`, src: image.dataUrl })),
    [referenceImages],
  );
  const imageModelLabel = imageModelOptions.find((option) => option.value === imageModel)?.label || imageModel;
  const imageAspectRatioLabel =
    IMAGE_ASPECT_RATIO_OPTIONS.find((option) => option.value === imageAspectRatio)?.label || "Auto";
  const imageResolutionLabel =
    IMAGE_RESOLUTION_OPTIONS.find((option) => option.value === imageResolution)?.label || "Auto";
  const imageQualityLabel =
    imageQualityOptions.find((option) => option.value === imageQuality)?.label || imageQuality;
  const supportsQuality = imageQualityOptions.length > 0;
  const submitLabel = composerMode === "chat" ? "发送对话" : referenceImages.length > 0 ? "编辑图片" : "生成图片";

  useEffect(() => {
    if (composerMode === "chat") {
      setIsImageSettingsOpen(false);
      setIsAspectRatioMenuOpen(false);
      setIsResolutionMenuOpen(false);
      setIsQualityMenuOpen(false);
      setIsOutputHintOpen(false);
    }
  }, [composerMode]);

  useEffect(() => {
    if (!supportsQuality) {
      setIsQualityMenuOpen(false);
      setIsOutputHintOpen(false);
    }
  }, [supportsQuality]);

  useEffect(() => {
    if (!isModelMenuOpen && !isAspectRatioMenuOpen && !isResolutionMenuOpen && !isQualityMenuOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!modelMenuRef.current?.contains(target)) {
        setIsModelMenuOpen(false);
      }
      if (!aspectRatioMenuRef.current?.contains(target)) {
        setIsAspectRatioMenuOpen(false);
      }
      if (!resolutionMenuRef.current?.contains(target)) {
        setIsResolutionMenuOpen(false);
      }
      if (!qualityMenuRef.current?.contains(target)) {
        setIsQualityMenuOpen(false);
      }
    };
    window.addEventListener("mousedown", handlePointerDown);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
    };
  }, [isAspectRatioMenuOpen, isModelMenuOpen, isQualityMenuOpen, isResolutionMenuOpen]);

  useEffect(() => {
    const handleResize = () => {
      setPromptAreaHeight((height) => clampPromptAreaHeight(height));
    };

    window.addEventListener("resize", handleResize);
    return () => {
      window.removeEventListener("resize", handleResize);
    };
  }, []);

  useEffect(() => {
    if (!isPromptAreaResizing) {
      return;
    }

    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    document.body.style.cursor = "ns-resize";
    document.body.style.userSelect = "none";
    return () => {
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
    };
  }, [isPromptAreaResizing]);

  const handleTextareaPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    if (composerMode === "chat") {
      return;
    }
    const imageFiles = getImageFiles(event.clipboardData.files);
    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    void onReferenceImageChange(imageFiles);
  };

  const addReferenceImages = (files: File[]) => {
    const imageFiles = getImageFiles(files);
    if (imageFiles.length === 0) {
      return;
    }

    if (composerMode === "chat") {
      onComposerModeChange("image");
    }
    void onReferenceImageChange(imageFiles);
  };

  const resetReferenceImageDragState = () => {
    referenceImageDragDepthRef.current = 0;
    setIsReferenceImageDragActive(false);
  };

  const handleReferenceImageDragEnter = (event: DragEvent<HTMLDivElement>) => {
    if (!hasDraggedImage(event.dataTransfer)) {
      return;
    }

    event.preventDefault();
    referenceImageDragDepthRef.current += 1;
    setIsReferenceImageDragActive(true);
    event.dataTransfer.dropEffect = "copy";
  };

  const handleReferenceImageDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!hasDraggedImage(event.dataTransfer)) {
      return;
    }

    event.preventDefault();
    setIsReferenceImageDragActive(true);
    event.dataTransfer.dropEffect = "copy";
  };

  const handleReferenceImageDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!hasDraggedImage(event.dataTransfer)) {
      return;
    }

    event.preventDefault();
    referenceImageDragDepthRef.current = Math.max(0, referenceImageDragDepthRef.current - 1);
    if (referenceImageDragDepthRef.current === 0) {
      setIsReferenceImageDragActive(false);
    }
  };

  const handleReferenceImageDrop = (event: DragEvent<HTMLDivElement>) => {
    if (!hasDraggedFiles(event.dataTransfer)) {
      return;
    }

    event.preventDefault();
    resetReferenceImageDragState();
    addReferenceImages(Array.from(event.dataTransfer.files));
  };

  const handlePromptResizeStart = (event: PointerEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    const handleRect = event.currentTarget.getBoundingClientRect();
    promptAreaResizeRef.current = {
      pointerOffsetY: event.clientY - handleRect.top,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setIsPromptAreaResizing(true);
  };

  const handlePromptResizeMove = (event: PointerEvent<HTMLButtonElement>) => {
    const resizeState = promptAreaResizeRef.current;
    if (!resizeState) {
      return;
    }

    event.preventDefault();
    const panelRect = composerPanelRef.current?.getBoundingClientRect();
    const toolbarHeight = composerToolbarRef.current?.getBoundingClientRect().height ?? 0;
    if (!panelRect) {
      return;
    }

    const handleHeight = event.currentTarget.getBoundingClientRect().height;
    const nextHeight = panelRect.bottom - toolbarHeight - handleHeight - event.clientY + resizeState.pointerOffsetY;
    setPromptAreaHeight(clampPromptAreaHeight(nextHeight));
  };

  const handlePromptResizeEnd = (event: PointerEvent<HTMLButtonElement>) => {
    if (!promptAreaResizeRef.current) {
      return;
    }

    promptAreaResizeRef.current = null;
    setIsPromptAreaResizing(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const handlePromptResizeKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setPromptAreaHeight((height) => clampPromptAreaHeight(height + PROMPT_AREA_KEYBOARD_STEP));
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setPromptAreaHeight((height) => clampPromptAreaHeight(height - PROMPT_AREA_KEYBOARD_STEP));
      return;
    }
    if (event.key === "Home") {
      event.preventDefault();
      setPromptAreaHeight(PROMPT_AREA_MIN_HEIGHT);
      return;
    }
    if (event.key === "End") {
      event.preventDefault();
      setPromptAreaHeight(getPromptAreaMaxHeight());
    }
  };

  const handlePickReferenceImage = () => {
    if (composerMode === "chat") {
      onComposerModeChange("image");
    }

    fileInputRef.current?.click();
  };

  const handleImageSettingsOpenChange = (open: boolean) => {
    setIsImageSettingsOpen(open);
    if (!open) {
      setIsAspectRatioMenuOpen(false);
      setIsResolutionMenuOpen(false);
      setIsQualityMenuOpen(false);
      setIsOutputHintOpen(false);
    }
  };

  return (
    <ImageComposerDock>
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        multiple
        className="hidden"
        onChange={(event) => {
          const files = Array.from(event.target.files || []);
          if (files.length === 0) {
            return;
          }
          addReferenceImages(files);
        }}
      />

      {composerMode === "image" && referenceImages.length > 0 ? (
        <div className="hide-scrollbar mb-2 flex max-h-20 gap-2 overflow-x-auto px-1 py-1 sm:mb-3">
          {referenceImages.map((image, index) => (
            <div key={`${image.name}-${index}`} className="relative size-14 shrink-0 sm:size-16">
              <button
                type="button"
                onClick={() => {
                  setLightboxIndex(index);
                  setLightboxOpen(true);
                }}
                className="group size-14 overflow-hidden rounded-xl border border-stone-200 bg-stone-50 transition hover:border-stone-300 sm:size-16"
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
                onClick={(event) => {
                  event.stopPropagation();
                  onRemoveReferenceImage(index);
                }}
                className="absolute -right-1 -top-1 z-10 inline-flex size-5 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 shadow-sm transition hover:border-stone-300 hover:text-stone-800"
                aria-label={`移除参考图 ${image.name || index + 1}`}
              >
                <X className="size-3" />
              </button>
            </div>
          ))}
        </div>
      ) : null}

      <div
        ref={composerPanelRef}
        className={cn(
          "relative overflow-visible rounded-[30px] border border-[#dedee3] bg-[#fffcff]/95 shadow-[0_20px_70px_-42px_rgba(15,23,42,0.5)] backdrop-blur-xl transition-colors dark:border-border dark:bg-card/95 dark:shadow-[0_24px_80px_-38px_rgba(0,0,0,0.78)] sm:rounded-[24px] sm:border-[#f2f3f5] sm:bg-white/95 sm:shadow-[0_24px_80px_-34px_rgba(15,23,42,0.42)] sm:dark:border-border sm:dark:bg-card/95",
          isReferenceImageDragActive &&
            "border-[#1456f0] bg-[#eef4ff]/95 dark:border-sky-500/70 dark:bg-sky-950/45 sm:border-[#1456f0] sm:bg-[#eef4ff]/95 sm:dark:border-sky-500/70 sm:dark:bg-sky-950/45",
        )}
        onDragEnter={handleReferenceImageDragEnter}
        onDragOver={handleReferenceImageDragOver}
        onDragLeave={handleReferenceImageDragLeave}
        onDrop={handleReferenceImageDrop}
      >
        {isReferenceImageDragActive ? (
          <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-[30px] border-2 border-dashed border-[#1456f0]/70 bg-white/70 text-sm font-medium text-[#1456f0] backdrop-blur-sm dark:border-sky-400/70 dark:bg-background/70 dark:text-sky-300 sm:rounded-[24px]">
            <span className="inline-flex items-center gap-2 rounded-full bg-white/90 px-4 py-2 shadow-[0_10px_30px_-18px_rgba(15,23,42,0.5)] dark:bg-card/90">
              <ImagePlus className="size-4" />
              松开上传图片
            </span>
          </div>
        ) : null}
        <button
          type="button"
          className={cn(
            "hidden h-4 w-full cursor-[ns-resize] touch-none select-none items-center justify-center rounded-t-[24px] focus-visible:outline-none sm:flex",
            isPromptAreaResizing && "cursor-row-resize",
          )}
          onPointerDown={handlePromptResizeStart}
          onPointerMove={handlePromptResizeMove}
          onPointerUp={handlePromptResizeEnd}
          onPointerCancel={handlePromptResizeEnd}
          onLostPointerCapture={() => {
            promptAreaResizeRef.current = null;
            setIsPromptAreaResizing(false);
          }}
          onKeyDown={handlePromptResizeKeyDown}
          aria-label="调整提示词输入区域高度"
          title="拖动调整输入区域高度"
        >
          <span className="h-1 w-10 rounded-full bg-[#8e8e93]/40 dark:bg-muted-foreground/35" />
        </button>
        <div
          className="cursor-text"
          onClick={() => {
            textareaRef.current?.focus();
          }}
        >
          <ImageLightbox
            images={lightboxImages}
            currentIndex={lightboxIndex}
            open={lightboxOpen}
            onOpenChange={setLightboxOpen}
            onIndexChange={setLightboxIndex}
          />
          <Textarea
            ref={textareaRef}
            value={prompt}
            onChange={(event) => onPromptChange(event.target.value)}
            onPaste={handleTextareaPaste}
            placeholder={
              composerMode === "chat"
                ? "输入消息与AI聊天"
                : referenceImages.length > 0
                ? "描述你希望如何修改参考图"
                : "输入你想要生成的画面，也可直接粘贴图片"
            }
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void onSubmit();
              }
            }}
            className="min-h-[96px] resize-none rounded-none border-0 bg-transparent px-6 pt-6 pb-2 text-[17px] leading-7 text-[#222222] shadow-none placeholder:text-[#8e8e93] focus-visible:ring-0 dark:text-foreground dark:placeholder:text-muted-foreground sm:min-h-0 sm:px-5 sm:py-4 sm:text-[15px] sm:leading-6"
            style={{ height: promptAreaHeight }}
          />

          <div
            ref={composerToolbarRef}
            className="rounded-b-[30px] bg-transparent px-3 pt-1 pb-3 sm:rounded-b-[24px] sm:border-t sm:border-[#f2f3f5] sm:bg-white/80 sm:px-4 sm:py-2.5 sm:dark:border-border sm:dark:bg-card/80"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 sm:gap-3">
              <div className="flex min-w-0 flex-nowrap items-center gap-1.5 sm:gap-2">
                <div className="inline-flex h-9 shrink-0 items-center rounded-full bg-transparent p-0 text-xs font-medium text-[#45515e] dark:text-muted-foreground sm:h-8 sm:bg-[#f0f0f0] sm:p-0.5 sm:dark:bg-muted/70">
                  {[
                    { value: "chat" as const, label: "对话", icon: MessageCircle },
                    { value: "image" as const, label: "作画", icon: ImageIcon },
                  ].map((option) => {
                    const Icon = option.icon;
                    const active = composerMode === option.value;
                    return (
                      <button
                        key={option.value}
                        type="button"
                        className={cn(
                          "inline-flex size-9 items-center justify-center gap-1.5 rounded-full transition sm:h-7 sm:w-auto sm:px-2.5",
                          active && option.value === "chat"
                            ? "bg-[#fff1f7] text-[#ea5ec1] dark:bg-rose-950/30 dark:text-pink-300 sm:bg-white sm:text-[#18181b] sm:shadow-sm sm:dark:bg-background sm:dark:text-foreground"
                            : active
                              ? "bg-[#eef4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300 sm:bg-white sm:text-[#18181b] sm:shadow-sm sm:dark:bg-background sm:dark:text-foreground"
                              : "text-[#686b73] hover:bg-black/[0.05] hover:text-[#18181b] dark:text-muted-foreground dark:hover:bg-accent/60 dark:hover:text-foreground sm:text-[#45515e] sm:hover:bg-transparent sm:dark:text-muted-foreground sm:dark:hover:bg-transparent",
                        )}
                        onClick={() => onComposerModeChange(option.value)}
                        aria-pressed={active}
                        aria-label={option.label}
                        title={option.label}
                      >
                        <Icon className="size-5 sm:size-3.5" />
                        <span className="hidden sm:inline">{option.label}</span>
                      </button>
                    );
                  })}
                </div>
                <div ref={modelMenuRef} className="relative shrink-0">
                  <button
                    type="button"
                    className={cn(
                      "inline-flex size-9 items-center justify-center gap-1.5 rounded-full text-xs font-medium text-[#686b73] transition hover:bg-black/[0.05] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#1456f0]/30 dark:text-muted-foreground dark:hover:bg-accent/60 dark:hover:text-foreground sm:h-8 sm:w-[190px] sm:border sm:border-[#e5e7eb] sm:bg-white sm:px-3 sm:text-[#45515e] sm:dark:border-border sm:dark:bg-background/70 sm:dark:text-muted-foreground",
                      isModelMenuOpen &&
                        "bg-[#eef4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300 sm:border-[#bfdbfe] sm:bg-[#eef4ff] sm:text-[#1456f0] sm:dark:border-sky-900/70 sm:dark:bg-sky-950/30 sm:dark:text-sky-300",
                    )}
                    onClick={() => {
                      setIsModelMenuOpen((open) => !open);
                      setIsAspectRatioMenuOpen(false);
                      setIsResolutionMenuOpen(false);
                      setIsQualityMenuOpen(false);
                    }}
                    aria-expanded={isModelMenuOpen}
                    aria-label={`选择模型，当前 ${imageModelLabel}`}
                    title={`模型：${imageModelLabel}`}
                  >
                    <Bot className="size-5 shrink-0 sm:hidden" />
                    <span className="hidden shrink-0 sm:inline">模型</span>
                    <span className="hidden min-w-0 flex-1 truncate text-left font-semibold sm:inline">
                      {imageModelLabel}
                    </span>
                    <ChevronDown className={cn("hidden size-4 shrink-0 opacity-60 transition sm:block", isModelMenuOpen && "rotate-180")} />
                  </button>
                  {isModelMenuOpen ? (
                    <div className="absolute bottom-[calc(100%+0.5rem)] left-0 z-[80] max-h-[45dvh] w-[min(14rem,calc(100vw-2rem))] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:shadow-[0_24px_80px_-28px_rgba(0,0,0,0.72)] sm:bottom-[calc(100%+8px)] sm:w-[218px]">
                      {imageModelOptions.map((option) => {
                        const active = option.value === imageModel;
                        return (
                          <button
                            key={option.value}
                            type="button"
                            className={cn(
                              "flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60",
                              active && "bg-black/[0.05] font-medium text-[#18181b] dark:bg-accent dark:text-foreground",
                            )}
                            onClick={() => {
                              onImageModelChange(option.value);
                              setIsModelMenuOpen(false);
                            }}
                          >
                            <span className="truncate">{option.label}</span>
                            {active ? <Check className="size-4 shrink-0" /> : null}
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
                <button
                  type="button"
                  className="inline-flex size-9 shrink-0 items-center justify-center gap-1.5 rounded-full text-[#686b73] transition hover:bg-black/[0.05] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#1456f0]/30 dark:text-muted-foreground dark:hover:bg-accent/60 dark:hover:text-foreground sm:h-8 sm:w-auto sm:border sm:border-[#e5e7eb] sm:bg-white sm:px-3 sm:text-xs sm:font-medium sm:text-[#45515e] sm:dark:border-border sm:dark:bg-background/70 sm:dark:text-muted-foreground"
                  onClick={onOpenPromptMarket}
                  aria-label="打开提示词市场"
                  title="提示词市场"
                >
                  <Store className="size-5 sm:size-3.5" />
                  <span className="hidden sm:inline">市场</span>
                </button>
                {composerMode === "image" ? (
                  <Popover open={isImageSettingsOpen} onOpenChange={handleImageSettingsOpenChange}>
                    <PopoverTrigger asChild>
                      <button
                        type="button"
                        className={cn(
                          "inline-flex size-9 shrink-0 items-center justify-center gap-1.5 rounded-full text-[#686b73] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60 dark:hover:text-foreground sm:h-8 sm:w-auto sm:border sm:border-[#e5e7eb] sm:bg-white sm:px-3 sm:text-xs sm:font-medium sm:text-[#45515e] sm:dark:border-border sm:dark:bg-background/70 sm:dark:text-muted-foreground",
                          isImageSettingsOpen && "bg-[#eef4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300 sm:border-[#bfdbfe] sm:bg-[#eef4ff] sm:text-[#1456f0] sm:dark:border-sky-900/70 sm:dark:bg-sky-950/30 sm:dark:text-sky-300",
                        )}
                        aria-label={isImageSettingsOpen ? "收起参数设置" : "显示更多参数设置"}
                        aria-expanded={isImageSettingsOpen}
                        title={isImageSettingsOpen ? "收起参数" : "更多参数"}
                      >
                        <SlidersHorizontal className="size-5 sm:size-3.5" />
                        <span className="hidden sm:inline">参数</span>
                      </button>
                    </PopoverTrigger>
                    <PopoverContent
                      align="start"
                      side="top"
                      sideOffset={8}
                      className="z-[70] w-[min(calc(100vw-2rem),22rem)] overflow-visible rounded-[20px] border-[#e5e7eb] bg-white p-2.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:shadow-[0_24px_80px_-28px_rgba(0,0,0,0.72)]"
                      onOpenAutoFocus={(event) => event.preventDefault()}
                    >
                      <div className={cn("grid gap-2", supportsQuality ? "grid-cols-2" : "grid-cols-1")}>
                      <div className="flex h-9 min-w-0 items-center justify-between gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 dark:border-border dark:bg-background/70">
                        <span className="shrink-0 text-[11px] font-medium text-[#45515e] dark:text-muted-foreground">张数</span>
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          max="10"
                          step="1"
                          value={imageCount}
                          onChange={(event) => onImageCountChange(event.target.value)}
                          className="h-7 w-[36px] border-0 bg-transparent px-0 text-center text-xs font-semibold text-[#18181b] shadow-none focus-visible:ring-0 dark:text-foreground"
                        />
                      </div>
                      <div
                        ref={aspectRatioMenuRef}
                        className="relative flex h-9 min-w-0 items-center justify-between gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] dark:border-border dark:bg-background/70"
                      >
                        <span className="shrink-0 font-medium text-[#45515e] dark:text-muted-foreground">比例</span>
                        <button
                          type="button"
                          className="flex h-7 min-w-0 flex-1 items-center justify-end gap-1 bg-transparent text-right text-xs font-semibold text-[#18181b] dark:text-foreground"
                          onClick={() => {
                            setIsAspectRatioMenuOpen((open) => !open);
                            setIsModelMenuOpen(false);
                            setIsResolutionMenuOpen(false);
                            setIsQualityMenuOpen(false);
                          }}
                        >
                          <span className="truncate">{imageAspectRatioLabel}</span>
                          <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isAspectRatioMenuOpen && "rotate-180")} />
                        </button>
                        {isAspectRatioMenuOpen ? (
                          <div className="absolute bottom-[calc(100%+0.5rem)] right-0 z-[90] max-h-[14rem] w-[min(17rem,calc(100vw-3rem))] overflow-y-auto rounded-[16px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_18px_46px_-26px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:shadow-[0_18px_46px_-24px_rgba(0,0,0,0.72)]">
                            {IMAGE_ASPECT_RATIO_OPTIONS.map((option) => {
                              const active = option.value === imageAspectRatio;
                              return (
                                <button
                                  key={option.label}
                                  type="button"
                                  className={cn(
                                    "flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60",
                                    active && "bg-black/[0.05] font-medium text-[#18181b] dark:bg-accent dark:text-foreground",
                                  )}
                                  onClick={() => {
                                    onImageAspectRatioChange(option.value);
                                    setIsAspectRatioMenuOpen(false);
                                  }}
                                >
                                  <span className="min-w-0 truncate">{option.label}</span>
                                  {active ? <Check className="size-4 shrink-0" /> : null}
                                </button>
                              );
                            })}
                          </div>
                        ) : null}
                      </div>
                      <div
                        ref={resolutionMenuRef}
                        className="relative flex h-9 min-w-0 items-center justify-between gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] dark:border-border dark:bg-background/70"
                      >
                        <span className="shrink-0 font-medium text-[#45515e] dark:text-muted-foreground">分辨率</span>
                        <button
                          type="button"
                          className="flex h-7 min-w-0 flex-1 items-center justify-end gap-1 bg-transparent text-right text-xs font-semibold text-[#18181b] dark:text-foreground"
                          onClick={() => {
                            setIsResolutionMenuOpen((open) => !open);
                            setIsModelMenuOpen(false);
                            setIsAspectRatioMenuOpen(false);
                            setIsQualityMenuOpen(false);
                          }}
                        >
                          <span className="truncate">{imageResolutionLabel}</span>
                          <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isResolutionMenuOpen && "rotate-180")} />
                        </button>
                        {isResolutionMenuOpen ? (
                          <div className="absolute bottom-[calc(100%+0.5rem)] left-0 z-[90] max-h-[14rem] w-[min(12rem,calc(100vw-3rem))] overflow-y-auto rounded-[16px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_18px_46px_-26px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:shadow-[0_18px_46px_-24px_rgba(0,0,0,0.72)]">
                            {IMAGE_RESOLUTION_OPTIONS.map((option) => {
                              const active = option.value === imageResolution;
                              return (
                                <button
                                  key={option.value}
                                  type="button"
                                  className={cn(
                                    "flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60",
                                    active && "bg-black/[0.05] font-medium text-[#18181b] dark:bg-accent dark:text-foreground",
                                  )}
                                  onClick={() => {
                                    onImageResolutionChange(option.value);
                                    setIsResolutionMenuOpen(false);
                                  }}
                                >
                                  <span className="min-w-0 truncate">{option.label}</span>
                                  {active ? <Check className="size-4 shrink-0" /> : null}
                                </button>
                              );
                            })}
                          </div>
                        ) : null}
                      </div>
                  {supportsQuality ? (
                      <div
                    ref={qualityMenuRef}
                    className="relative flex h-9 min-w-0 items-center justify-between gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] dark:border-border dark:bg-background/70"
                  >
                    <span className="flex shrink-0 items-center gap-1 font-medium text-[#45515e] dark:text-muted-foreground">
                      质量
                      <Popover open={isOutputHintOpen} onOpenChange={setIsOutputHintOpen}>
                        <PopoverTrigger asChild>
                          <button
                            type="button"
                            className="inline-flex size-4 shrink-0 items-center justify-center text-[#8e8e93] transition hover:text-[#45515e] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:text-muted-foreground dark:hover:text-foreground"
                            aria-label="查看图片输出说明"
                          >
                            <CircleHelp className="size-3.5" />
                          </button>
                        </PopoverTrigger>
                        <PopoverContent
                          align="center"
                          side="top"
                          sideOffset={6}
                          className="z-[120] w-[min(calc(100vw-2rem),20rem)] rounded-xl border-[#e5e7eb] bg-white px-4 py-3 text-xs leading-6 text-[#45515e] shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:text-muted-foreground dark:shadow-[0_24px_80px_-28px_rgba(0,0,0,0.72)]"
                          onOpenAutoFocus={(event) => event.preventDefault()}
                        >
                          {imageOutputHint}
                        </PopoverContent>
                      </Popover>
                    </span>
                    <button
                      type="button"
                      className="flex h-7 min-w-0 flex-1 items-center justify-end gap-1 bg-transparent text-right text-xs font-semibold text-[#18181b] dark:text-foreground"
                      onClick={() => {
                        setIsQualityMenuOpen((open) => !open);
                        setIsModelMenuOpen(false);
                        setIsAspectRatioMenuOpen(false);
                        setIsResolutionMenuOpen(false);
                      }}
                      title={imageQualityOptions.find((option) => option.value === imageQuality)?.description}
                    >
                      <span className="truncate">{imageQualityLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isQualityMenuOpen && "rotate-180")} />
                    </button>
                    {isQualityMenuOpen ? (
                      <div className="absolute bottom-[calc(100%+0.5rem)] right-0 z-[90] max-h-[14rem] w-[min(17rem,calc(100vw-3rem))] overflow-y-auto rounded-[16px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_18px_46px_-26px_rgba(15,23,42,0.35)] dark:border-border dark:bg-card dark:shadow-[0_18px_46px_-24px_rgba(0,0,0,0.72)]">
                        {imageQualityOptions.map((option) => {
                          const active = option.value === imageQuality;
                          return (
                            <button
                              key={option.value}
                              type="button"
                              className={cn(
                                "flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60",
                                active && "bg-black/[0.05] font-medium text-[#18181b] dark:bg-accent dark:text-foreground",
                              )}
                              title={option.description}
                              onClick={() => {
                                onImageQualityChange(option.value);
                                setIsQualityMenuOpen(false);
                              }}
                            >
                              <span className="min-w-0">
                                <span className="block truncate">{option.label}</span>
                                <span className="block truncate text-[11px] font-normal text-[#8e8e93] dark:text-muted-foreground">
                                  {option.description}
                                </span>
                              </span>
                              {active ? <Check className="size-4 shrink-0" /> : null}
                            </button>
                          );
                        })}
                      </div>
                    ) : null}
                      </div>
                  ) : null}
                      </div>
                    </PopoverContent>
                  </Popover>
                  ) : null}
              </div>

              <div className="flex shrink-0 items-center gap-2">
                <button
                  type="button"
                  onClick={handlePickReferenceImage}
                  className="inline-flex size-11 items-center justify-center rounded-full text-[#686b73] transition hover:bg-black/[0.05] dark:text-muted-foreground dark:hover:bg-accent/60 dark:hover:text-foreground sm:size-10 sm:border sm:border-[#e5e7eb] sm:bg-white sm:text-[#45515e] sm:dark:border-border sm:dark:bg-background/70 sm:dark:text-muted-foreground"
                  aria-label="上传参考图"
                  title="上传参考图"
                >
                  <Plus className="size-6 sm:hidden" />
                  <ImagePlus className="hidden size-4 sm:block" />
                </button>

                <button
                  type="button"
                  onClick={() => void onSubmit()}
                  disabled={!prompt.trim()}
                  className="inline-flex size-11 shrink-0 items-center justify-center rounded-full bg-[#181e25] text-white shadow-[0_4px_10px_rgba(24,30,37,0.12)] transition hover:bg-[#2a323d] disabled:cursor-not-allowed disabled:bg-[#e1e2e4] disabled:text-[#73777f] dark:bg-foreground dark:text-background dark:hover:bg-foreground/90 dark:disabled:bg-muted dark:disabled:text-muted-foreground sm:size-10"
                  aria-label={submitLabel}
                  title={submitLabel}
                >
                  <ArrowUp className="size-5 sm:size-4" />
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </ImageComposerDock>
  );
}
