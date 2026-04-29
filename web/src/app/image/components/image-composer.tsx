"use client";
import { ArrowUp, Check, ChevronDown, ImagePlus, LoaderCircle, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ClipboardEvent, type ReactNode, type RefObject } from "react";

import { ImageLightbox } from "@/components/image-lightbox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { IMAGE_SIZE_OPTIONS } from "@/app/image/image-options";
import type { ImageModel } from "@/lib/api";
import { cn } from "@/lib/utils";

type ImageComposerProps = {
  prompt: string;
  imageCount: string;
  imageModel: ImageModel;
  imageModelOptions: ReadonlyArray<{ value: ImageModel; label: string }>;
  imageSize: string;
  availableQuota: string;
  activeTaskCount: number;
  referenceImages: Array<{ name: string; dataUrl: string }>;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  fileInputRef: RefObject<HTMLInputElement | null>;
  onPromptChange: (value: string) => void;
  onImageCountChange: (value: string) => void;
  onImageModelChange: (value: ImageModel) => void;
  onImageSizeChange: (value: string) => void;
  onSubmit: () => void | Promise<void>;
  onPickReferenceImage: () => void;
  onReferenceImageChange: (files: File[]) => void | Promise<void>;
  onRemoveReferenceImage: (index: number) => void;
};

function ImageComposerDock({ children }: { children: ReactNode }) {
  return (
    <div className="shrink-0 px-1 sm:px-0">
      <div className="mx-auto w-full max-w-[900px]">{children}</div>
    </div>
  );
}

export function ImageComposer({
  prompt,
  imageCount,
  imageModel,
  imageModelOptions,
  imageSize,
  availableQuota,
  activeTaskCount,
  referenceImages,
  textareaRef,
  fileInputRef,
  onPromptChange,
  onImageCountChange,
  onImageModelChange,
  onImageSizeChange,
  onSubmit,
  onPickReferenceImage,
  onReferenceImageChange,
  onRemoveReferenceImage,
}: ImageComposerProps) {
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [isModelMenuOpen, setIsModelMenuOpen] = useState(false);
  const [isSizeMenuOpen, setIsSizeMenuOpen] = useState(false);
  const modelMenuRef = useRef<HTMLDivElement>(null);
  const sizeMenuRef = useRef<HTMLDivElement>(null);
  const lightboxImages = useMemo(
    () => referenceImages.map((image, index) => ({ id: `${image.name}-${index}`, src: image.dataUrl })),
    [referenceImages],
  );
  const imageModelLabel = imageModelOptions.find((option) => option.value === imageModel)?.label || imageModel;
  const imageSizeLabel = IMAGE_SIZE_OPTIONS.find((option) => option.value === imageSize)?.label || "未指定";

  useEffect(() => {
    if (!isModelMenuOpen && !isSizeMenuOpen) {
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
    };
    window.addEventListener("mousedown", handlePointerDown);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
    };
  }, [isModelMenuOpen, isSizeMenuOpen]);

  const handleTextareaPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const imageFiles = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    void onReferenceImageChange(imageFiles);
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
          void onReferenceImageChange(Array.from(event.target.files || []));
        }}
      />

      {referenceImages.length > 0 ? (
        <div className="mb-2 flex gap-2 overflow-x-auto px-1 pb-1 sm:mb-3 sm:flex-wrap sm:overflow-visible sm:pb-0">
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

      <div className="overflow-visible rounded-[24px] border border-[#f2f3f5] bg-white shadow-[0_0_15px_rgba(44,30,116,0.16)]">
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
              referenceImages.length > 0
                ? "描述你希望如何修改参考图"
                : "输入你想要生成的画面，也可直接粘贴图片"
            }
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void onSubmit();
              }
            }}
            className="min-h-[74px] max-h-[170px] resize-y rounded-t-[24px] rounded-b-none border-0 bg-transparent px-4 py-3 text-[15px] leading-6 text-[#222222] shadow-none placeholder:text-[#8e8e93] focus-visible:ring-0 sm:min-h-[104px] sm:px-5 sm:py-4"
          />

          <div
            className="rounded-b-[24px] border-t border-[#f2f3f5] bg-white px-3 py-2.5 sm:px-4"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2 sm:items-center sm:gap-3">
              <div className="flex min-w-0 flex-wrap items-center gap-1.5 sm:gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    className="h-8 shrink-0 rounded-full border-[#e5e7eb] bg-white px-3 text-xs font-medium text-[#45515e] shadow-none hover:bg-black/[0.05]"
                    onClick={onPickReferenceImage}
                  >
                    <ImagePlus className="size-3.5" />
                    <span>上传</span>
                  </Button>
                  <div className="inline-flex h-8 shrink-0 items-center whitespace-nowrap rounded-full bg-[#f0f0f0] px-3 text-[10px] font-medium text-[#45515e] sm:text-xs">
                    <span>剩余额度</span> {availableQuota}
                  </div>
                  {activeTaskCount > 0 && (
                    <div className="flex h-8 shrink-0 items-center gap-1 rounded-full bg-amber-50 px-3 text-[10px] font-medium text-amber-700 ring-1 ring-amber-100 sm:text-xs">
                      <LoaderCircle className="size-3 animate-spin" />
                      {activeTaskCount}<span className="hidden sm:inline"> 个任务处理中</span>
                    </div>
                  )}
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
                      }}
                    >
                      <span className="truncate">{imageModelLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isModelMenuOpen && "rotate-180")} />
                    </button>
                    {isModelMenuOpen ? (
                      <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+5.25rem)] z-[80] max-h-[45dvh] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] sm:absolute sm:inset-x-auto sm:bottom-[calc(100%+8px)] sm:left-0 sm:w-[218px]">
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
                      }}
                    >
                      <span className="truncate">{imageSizeLabel}</span>
                      <ChevronDown className={cn("size-4 shrink-0 opacity-60 transition", isSizeMenuOpen && "rotate-180")} />
                    </button>
                    {isSizeMenuOpen ? (
                      <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+5.25rem)] z-[80] max-h-[45dvh] overflow-y-auto rounded-[20px] border border-[#e5e7eb] bg-white p-1.5 shadow-[0_24px_80px_-32px_rgba(15,23,42,0.35)] sm:absolute sm:inset-x-auto sm:bottom-[calc(100%+8px)] sm:left-0 sm:w-[186px]">
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
              </div>

              <button
                type="button"
                onClick={() => void onSubmit()}
                disabled={!prompt.trim()}
                className="inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-[#181e25] text-white shadow-[0_4px_10px_rgba(24,30,37,0.12)] transition hover:bg-[#2a323d] disabled:cursor-not-allowed disabled:bg-[#d1d5db] sm:size-10"
                aria-label={referenceImages.length > 0 ? "编辑图片" : "生成图片"}
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
