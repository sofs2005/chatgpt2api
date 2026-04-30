"use client";
import { ArrowUp, Check, ChevronDown, CircleHelp, Image as ImageIcon, ImagePlus, MessageCircle, Store, X } from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ClipboardEvent,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
  type RefObject,
} from "react";

import { ImageLightbox } from "@/components/image-lightbox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { IMAGE_SIZE_OPTIONS } from "@/app/image/image-options";
import type { ImageModel, ImageQuality } from "@/lib/api";
import { cn } from "@/lib/utils";

type ImageComposerProps = {
  composerMode: "chat" | "image";
  prompt: string;
  imageCount: string;
  imageModel: ImageModel;
  imageModelOptions: ReadonlyArray<{ value: ImageModel; label: string }>;
  imageSize: string;
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
  onImageSizeChange: (value: string) => void;
  onImageQualityChange: (value: ImageQuality) => void;
  onSubmit: () => void | Promise<void>;
  onPickReferenceImage: () => void;
  onOpenPromptMarket: () => void;
  onReferenceImageChange: (files: File[]) => void | Promise<void>;
  onRemoveReferenceImage: (index: number) => void;
};

const PROMPT_AREA_MIN_HEIGHT = 74;
const PROMPT_AREA_DEFAULT_HEIGHT = 104;
const PROMPT_AREA_MAX_HEIGHT = 320;
const PROMPT_AREA_KEYBOARD_STEP = 16;

function getPromptAreaMaxHeight() {
  if (typeof window === "undefined") {
    return PROMPT_AREA_MAX_HEIGHT;
  }
  return Math.max(PROMPT_AREA_MIN_HEIGHT, Math.min(PROMPT_AREA_MAX_HEIGHT, Math.floor(window.innerHeight * 0.42)));
}

function clampPromptAreaHeight(height: number) {
  return Math.min(Math.max(height, PROMPT_AREA_MIN_HEIGHT), getPromptAreaMaxHeight());
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
  imageSize,
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
  onImageSizeChange,
  onImageQualityChange,
  onSubmit,
  onPickReferenceImage,
  onOpenPromptMarket,
  onReferenceImageChange,
  onRemoveReferenceImage,
}: ImageComposerProps) {
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [isModelMenuOpen, setIsModelMenuOpen] = useState(false);
  const [isSizeMenuOpen, setIsSizeMenuOpen] = useState(false);
  const [isQualityMenuOpen, setIsQualityMenuOpen] = useState(false);
  const [promptAreaHeight, setPromptAreaHeight] = useState(PROMPT_AREA_DEFAULT_HEIGHT);
  const [isPromptAreaResizing, setIsPromptAreaResizing] = useState(false);
  const composerPanelRef = useRef<HTMLDivElement>(null);
  const composerToolbarRef = useRef<HTMLDivElement>(null);
  const modelMenuRef = useRef<HTMLDivElement>(null);
  const sizeMenuRef = useRef<HTMLDivElement>(null);
  const qualityMenuRef = useRef<HTMLDivElement>(null);
  const promptAreaResizeRef = useRef<{ pointerOffsetY: number } | null>(null);
  const lightboxImages = useMemo(
    () => referenceImages.map((image, index) => ({ id: `${image.name}-${index}`, src: image.dataUrl })),
    [referenceImages],
  );
  const imageModelLabel = imageModelOptions.find((option) => option.value === imageModel)?.label || imageModel;
  const imageSizeLabel = IMAGE_SIZE_OPTIONS.find((option) => option.value === imageSize)?.label || "未指定";
  const imageQualityLabel =
    imageQualityOptions.find((option) => option.value === imageQuality)?.label || imageQuality;

  useEffect(() => {
    if (!isModelMenuOpen && !isSizeMenuOpen && !isQualityMenuOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!modelMenuRef.current?.contains(target)) {
        setIsModelMenuOpen(false);
      }
      if (!sizeMenuRef.current?.contains(target)) {
        setIsSizeMenuOpen(false);
      }
      if (!qualityMenuRef.current?.contains(target)) {
        setIsQualityMenuOpen(false);
      }
    };
    window.addEventListener("mousedown", handlePointerDown);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
    };
  }, [isModelMenuOpen, isQualityMenuOpen, isSizeMenuOpen]);

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
    const imageFiles = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    void onReferenceImageChange(imageFiles);
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

  return (
    <ImageComposerDock>
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        multiple
        className="hidden"
        onChange={(event) => {
          if (composerMode === "image") {
            void onReferenceImageChange(Array.from(event.target.files || []));
          }
        }}
      />

      {composerMode === "image" && referenceImages.length > 0 ? (
        <div className="hide-scrollbar mb-2 flex max-h-[4.5rem] gap-2 overflow-x-auto px-1 pb-1 sm:mb-3">
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
                className="absolute -right-1 -top-1 inline-flex size-5 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 transition hover:border-stone-300 hover:text-stone-800"
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
        className="overflow-visible rounded-[24px] border border-[#f2f3f5] bg-white/95 shadow-[0_24px_80px_-34px_rgba(15,23,42,0.42)] backdrop-blur-xl"
      >
        <button
          type="button"
          className={cn(
            "flex h-4 w-full cursor-[ns-resize] touch-none select-none items-center justify-center rounded-t-[24px] focus-visible:outline-none",
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
          <span className="h-1 w-10 rounded-full bg-[#8e8e93]/40" />
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
                ? "输入你想聊的内容"
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
            className="resize-none rounded-none border-0 bg-transparent px-4 py-3 text-[15px] leading-6 text-[#222222] shadow-none placeholder:text-[#8e8e93] focus-visible:ring-0 sm:px-5 sm:py-4"
            style={{ height: promptAreaHeight }}
          />

          <div
            ref={composerToolbarRef}
            className="rounded-b-[24px] border-t border-[#f2f3f5] bg-white/80 px-3 py-2.5 sm:px-4"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2 sm:items-center sm:gap-3">
              <div className="flex min-w-0 flex-wrap items-center gap-1.5 sm:gap-2">
                <div className="inline-flex h-8 shrink-0 items-center rounded-full bg-[#f0f0f0] p-0.5 text-xs font-medium text-[#45515e]">
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
                          "inline-flex h-7 items-center gap-1.5 rounded-full px-2.5 transition",
                          active ? "bg-white text-[#18181b] shadow-sm" : "text-[#45515e] hover:text-[#18181b]",
                        )}
                        onClick={() => onComposerModeChange(option.value)}
                        aria-pressed={active}
                      >
                        <Icon className="size-3.5" />
                        <span>{option.label}</span>
                      </button>
                    );
                  })}
                </div>
                {composerMode === "image" ? (
                  <>
                    <Button
                      type="button"
                      variant="outline"
                      className="h-8 shrink-0 rounded-full border-[#e5e7eb] bg-white px-3 text-xs font-medium text-[#45515e] shadow-none hover:bg-black/[0.05]"
                      onClick={onOpenPromptMarket}
                    >
                      <Store className="size-3.5" />
                      <span>市场</span>
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      className="h-8 shrink-0 rounded-full border-[#e5e7eb] bg-white px-3 text-xs font-medium text-[#45515e] shadow-none hover:bg-black/[0.05]"
                      onClick={onPickReferenceImage}
                    >
                      <ImagePlus className="size-3.5" />
                      <span>上传</span>
                    </Button>
                  </>
                ) : null}
                <div
                  ref={modelMenuRef}
                  className="relative flex h-8 min-w-0 items-center gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] sm:text-xs"
                >
                    <span className="font-medium text-[#45515e]">模型</span>
                    <button
                      type="button"
                      className="flex h-7 w-[86px] items-center justify-between bg-transparent text-left text-xs font-semibold text-[#18181b] min-[390px]:w-[112px] sm:w-[148px]"
                      onClick={() => {
                        setIsModelMenuOpen((open) => !open);
                        setIsSizeMenuOpen(false);
                        setIsQualityMenuOpen(false);
                      }}
                    >
                      <span className="truncate">{imageModelLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isModelMenuOpen && "rotate-180")} />
                    </button>
                    {isModelMenuOpen ? (
                      <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+var(--image-composer-dock-height)+0.75rem)] z-[80] max-h-[45dvh] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] sm:absolute sm:inset-x-auto sm:bottom-[calc(100%+8px)] sm:left-0 sm:w-[218px]">
                        {imageModelOptions.map((option) => {
                          const active = option.value === imageModel;
                          return (
                            <button
                              key={option.value}
                              type="button"
                              className={cn(
                                "flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05]",
                                active && "bg-black/[0.05] font-medium text-[#18181b]",
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
                  {composerMode === "image" ? (
                    <>
                      <div className="flex h-8 shrink-0 items-center gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5">
                        <span className="text-[11px] font-medium text-[#45515e] sm:text-xs">张数</span>
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          max="10"
                          step="1"
                          value={imageCount}
                          onChange={(event) => onImageCountChange(event.target.value)}
                          className="h-7 w-[36px] border-0 bg-transparent px-0 text-center text-xs font-semibold text-[#18181b] shadow-none focus-visible:ring-0 sm:w-[46px]"
                        />
                      </div>
                  <div
                    ref={sizeMenuRef}
                    className="relative flex h-8 min-w-0 items-center gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] sm:text-xs"
                  >
                    <span className="font-medium text-[#45515e]">比例</span>
                    <button
                      type="button"
                      className="flex h-7 w-[78px] items-center justify-between bg-transparent text-left text-xs font-semibold text-[#18181b] min-[390px]:w-[96px] sm:w-[126px]"
                      onClick={() => {
                        setIsSizeMenuOpen((open) => !open);
                        setIsModelMenuOpen(false);
                        setIsQualityMenuOpen(false);
                      }}
                    >
                      <span className="truncate">{imageSizeLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isSizeMenuOpen && "rotate-180")} />
                    </button>
                    {isSizeMenuOpen ? (
                      <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+var(--image-composer-dock-height)+0.75rem)] z-[80] max-h-[45dvh] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] sm:absolute sm:inset-x-auto sm:bottom-[calc(100%+8px)] sm:left-0 sm:w-[186px]">
                        {IMAGE_SIZE_OPTIONS.map((option) => {
                          const active = option.value === imageSize;
                          return (
                            <button
                              key={option.label}
                              type="button"
                              className={cn(
                                "flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05]",
                                active && "bg-black/[0.05] font-medium text-[#18181b]",
                              )}
                              onClick={() => {
                                onImageSizeChange(option.value);
                                setIsSizeMenuOpen(false);
                              }}
                            >
                              <span>{option.label}</span>
                              {active ? <Check className="size-4" /> : null}
                            </button>
                          );
                        })}
                      </div>
                    ) : null}
                  </div>
                  <Popover>
                    <PopoverTrigger asChild>
                      <button
                        type="button"
                        className="inline-flex size-8 shrink-0 items-center justify-center rounded-full border border-[#e5e7eb] bg-white text-[#8e8e93] transition hover:bg-black/[0.05] hover:text-[#45515e]"
                        aria-label="查看图片输出说明"
                        title="查看图片输出说明"
                      >
                        <CircleHelp className="size-4" />
                      </button>
                    </PopoverTrigger>
                    <PopoverContent
                      align="end"
                      side="top"
                      className="w-[min(calc(100vw-2rem),20rem)] rounded-[18px] border-[#e5e7eb] px-4 py-3 text-xs leading-6 text-[#45515e] shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)]"
                    >
                      {imageOutputHint}
                    </PopoverContent>
                  </Popover>
                  <div
                    ref={qualityMenuRef}
                    className="relative flex h-8 min-w-0 items-center gap-1.5 rounded-full border border-[#e5e7eb] bg-white px-2.5 text-[11px] sm:text-xs"
                  >
                    <span className="font-medium text-[#45515e]">质量</span>
                    <button
                      type="button"
                      className="flex h-7 w-[76px] items-center justify-between bg-transparent text-left text-xs font-semibold text-[#18181b] sm:w-[94px]"
                      onClick={() => {
                        setIsQualityMenuOpen((open) => !open);
                        setIsModelMenuOpen(false);
                        setIsSizeMenuOpen(false);
                      }}
                      title={imageQualityOptions.find((option) => option.value === imageQuality)?.description}
                    >
                      <span className="truncate">{imageQualityLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isQualityMenuOpen && "rotate-180")} />
                    </button>
                    {isQualityMenuOpen ? (
                      <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+var(--image-composer-dock-height)+0.75rem)] z-[80] max-h-[45dvh] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] sm:absolute sm:inset-x-auto sm:bottom-[calc(100%+8px)] sm:left-0 sm:w-[224px]">
                        {imageQualityOptions.map((option) => {
                          const active = option.value === imageQuality;
                          return (
                            <button
                              key={option.value}
                              type="button"
                              className={cn(
                                "flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm text-[#45515e] transition hover:bg-black/[0.05]",
                                active && "bg-black/[0.05] font-medium text-[#18181b]",
                              )}
                              title={option.description}
                              onClick={() => {
                                onImageQualityChange(option.value);
                                setIsQualityMenuOpen(false);
                              }}
                            >
                              <span className="min-w-0">
                                <span className="block truncate">{option.label}</span>
                                <span className="block truncate text-[11px] font-normal text-[#8e8e93]">
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
                  </>
                  ) : null}
              </div>

              <button
                type="button"
                onClick={() => void onSubmit()}
                disabled={!prompt.trim()}
                className="inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-[#181e25] text-white shadow-[0_4px_10px_rgba(24,30,37,0.12)] transition hover:bg-[#2a323d] disabled:cursor-not-allowed disabled:bg-[#d1d5db] sm:size-10"
                aria-label={
                  composerMode === "chat" ? "发送对话" : referenceImages.length > 0 ? "编辑图片" : "生成图片"
                }
              >
                <ArrowUp className="size-3.5 sm:size-4" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </ImageComposerDock>
  );
}
