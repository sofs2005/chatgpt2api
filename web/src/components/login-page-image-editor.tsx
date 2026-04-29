"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { RotateCcw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { DEFAULT_LOGIN_PAGE_IMAGE, resolveLoginPageImageSrc } from "@/lib/app-meta";
import {
  getLoginPageImageLayout,
  getLoginPageImagePositionPercentFromOffset,
  LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM,
  LOGIN_PAGE_IMAGE_MAX_ZOOM,
  LOGIN_PAGE_IMAGE_MIN_ZOOM,
  normalizeLoginPageImageMode,
  normalizeLoginPageImageTransform,
  type LoginPageImageMode,
} from "@/lib/login-page-image-layout";

type LoginPageImageTransform = {
  zoom: number;
  positionX: number;
  positionY: number;
};

type LoginPageImageEditorProps = {
  mode?: LoginPageImageMode | string;
  onChange?: (transform: LoginPageImageTransform) => void;
  positionX?: number;
  positionY?: number;
  src?: string;
  zoom?: number;
};

const zoomStep = 0.08;

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}

export function LoginPageImageEditor({
  mode = "contain",
  onChange,
  positionX = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX,
  positionY = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY,
  src,
  zoom = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
}: LoginPageImageEditorProps) {
  const frameRef = useRef<HTMLDivElement | null>(null);
  const dragStateRef = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    initialPositionX: number;
    initialPositionY: number;
    initialOffsetX: number;
    initialOffsetY: number;
  } | null>(null);

  const [failedSrc, setFailedSrc] = useState("");
  const [imageSize, setImageSize] = useState({ width: 0, height: 0 });
  const [frameSize, setFrameSize] = useState({ width: 0, height: 0 });
  const resolvedMode = normalizeLoginPageImageMode(mode);
  const resolvedSrc = resolveLoginPageImageSrc(src);
  const fallbackSrc = resolveLoginPageImageSrc(DEFAULT_LOGIN_PAGE_IMAGE);
  const currentSrc = failedSrc === resolvedSrc ? fallbackSrc : resolvedSrc;
  const transform = useMemo(
    () => normalizeLoginPageImageTransform({ zoom, positionX, positionY }),
    [positionX, positionY, zoom],
  );
  const imageLayout = useMemo(
    () =>
      getLoginPageImageLayout({
        frameWidth: frameSize.width,
        frameHeight: frameSize.height,
        imageWidth: imageSize.width,
        imageHeight: imageSize.height,
        mode: resolvedMode,
        zoom: transform.zoom,
        positionX: transform.positionX,
        positionY: transform.positionY,
      }),
    [
      frameSize.height,
      frameSize.width,
      imageSize.height,
      imageSize.width,
      resolvedMode,
      transform.positionX,
      transform.positionY,
      transform.zoom,
    ],
  );

  useEffect(() => {
    const frame = frameRef.current;
    if (!frame) {
      return undefined;
    }

    const updateFrameSize = () => {
      const nextWidth = frame.clientWidth;
      const nextHeight = frame.clientHeight;
      setFrameSize((current) =>
        current.width === nextWidth && current.height === nextHeight
          ? current
          : { width: nextWidth, height: nextHeight },
      );
    };

    updateFrameSize();
    const observer = new ResizeObserver(updateFrameSize);
    observer.observe(frame);
    return () => observer.disconnect();
  }, []);

  const commitTransform = useCallback(
    (patch: Partial<LoginPageImageTransform> = {}) => {
      onChange?.(normalizeLoginPageImageTransform({ ...transform, ...patch }));
    },
    [onChange, transform],
  );

  const applyZoomAtPoint = useCallback(
    (nextZoom: number, pointX: number, pointY: number) => {
      const clampedZoom = clamp(nextZoom, LOGIN_PAGE_IMAGE_MIN_ZOOM, LOGIN_PAGE_IMAGE_MAX_ZOOM);

      if (!imageLayout) {
        commitTransform({ zoom: clampedZoom });
        return;
      }

      const safeImagePointX = imageLayout.width ? (pointX - imageLayout.x) / imageLayout.width : 0.5;
      const safeImagePointY = imageLayout.height ? (pointY - imageLayout.y) / imageLayout.height : 0.5;
      const nextLayout = getLoginPageImageLayout({
        frameWidth: frameSize.width,
        frameHeight: frameSize.height,
        imageWidth: imageSize.width,
        imageHeight: imageSize.height,
        mode: resolvedMode,
        zoom: clampedZoom,
        positionX: transform.positionX,
        positionY: transform.positionY,
      });

      if (!nextLayout) {
        commitTransform({ zoom: clampedZoom });
        return;
      }

      commitTransform({
        zoom: clampedZoom,
        positionX: getLoginPageImagePositionPercentFromOffset(
          pointX - safeImagePointX * nextLayout.width,
          nextLayout.availableX,
          transform.positionX,
        ),
        positionY: getLoginPageImagePositionPercentFromOffset(
          pointY - safeImagePointY * nextLayout.height,
          nextLayout.availableY,
          transform.positionY,
        ),
      });
    },
    [
      commitTransform,
      frameSize.height,
      frameSize.width,
      imageLayout,
      imageSize.height,
      imageSize.width,
      resolvedMode,
      transform.positionX,
      transform.positionY,
    ],
  );

  useEffect(() => {
    const frame = frameRef.current;
    if (!frame) {
      return undefined;
    }

    const handleWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = frame.getBoundingClientRect();
      applyZoomAtPoint(
        transform.zoom + (event.deltaY < 0 ? zoomStep : -zoomStep),
        event.clientX - rect.left,
        event.clientY - rect.top,
      );
    };

    frame.addEventListener("wheel", handleWheel, { passive: false });
    return () => frame.removeEventListener("wheel", handleWheel);
  }, [applyZoomAtPoint, transform.zoom]);

  const imageStyle: CSSProperties | undefined =
    imageLayout
      ? {
          width: `${imageLayout.width}px`,
          height: `${imageLayout.height}px`,
          transform: `translate(${imageLayout.x}px, ${imageLayout.y}px)`,
          transformOrigin: "top left",
        }
      : {
          inset: 0,
          width: "100%",
          height: "100%",
          objectFit: resolvedMode === "fill" ? "fill" : resolvedMode,
          objectPosition: `${transform.positionX}% ${transform.positionY}%`,
          transform: `scale(${transform.zoom})`,
          transformOrigin: "center center",
        };

  return (
    <div className="flex flex-col gap-3">
      <div
        ref={frameRef}
        className="relative mx-auto flex aspect-[10/13] w-full max-w-[20rem] touch-none items-stretch justify-stretch overflow-hidden rounded-2xl border bg-stone-100/60 shadow-inner dark:bg-muted/10"
        onPointerDown={(event) => {
          dragStateRef.current = {
            pointerId: event.pointerId,
            startX: event.clientX,
            startY: event.clientY,
            initialPositionX: transform.positionX,
            initialPositionY: transform.positionY,
            initialOffsetX: imageLayout?.x ?? 0,
            initialOffsetY: imageLayout?.y ?? 0,
          };
          event.currentTarget.setPointerCapture(event.pointerId);
        }}
        onPointerMove={(event) => {
          const dragState = dragStateRef.current;
          if (!dragState || dragState.pointerId !== event.pointerId) {
            return;
          }

          const deltaX = event.clientX - dragState.startX;
          const deltaY = event.clientY - dragState.startY;
          if (!imageLayout) {
            return;
          }
          commitTransform({
            positionX: getLoginPageImagePositionPercentFromOffset(
              dragState.initialOffsetX + deltaX,
              imageLayout.availableX,
              dragState.initialPositionX,
            ),
            positionY: getLoginPageImagePositionPercentFromOffset(
              dragState.initialOffsetY + deltaY,
              imageLayout.availableY,
              dragState.initialPositionY,
            ),
          });
        }}
        onPointerUp={(event) => {
          if (dragStateRef.current?.pointerId === event.pointerId) {
            dragStateRef.current = null;
            event.currentTarget.releasePointerCapture(event.pointerId);
          }
        }}
        onPointerCancel={(event) => {
          if (dragStateRef.current?.pointerId === event.pointerId) {
            dragStateRef.current = null;
            event.currentTarget.releasePointerCapture(event.pointerId);
          }
        }}
      >
        <img
          src={currentSrc}
          alt="登录页图片预览"
          className="absolute top-0 left-0 max-w-none select-none"
          draggable={false}
          onLoad={(event) => {
            const target = event.currentTarget;
            const nextImageSize = {
              width: target.naturalWidth,
              height: target.naturalHeight,
            };
            setImageSize((current) =>
              current.width === nextImageSize.width && current.height === nextImageSize.height ? current : nextImageSize,
            );
          }}
          onError={(event) => {
            if (event.currentTarget.src !== fallbackSrc) {
              event.currentTarget.src = fallbackSrc;
              setFailedSrc(resolvedSrc);
            }
          }}
          style={imageStyle}
        />
        <div className="pointer-events-none absolute inset-x-0 top-0 h-16 bg-linear-to-b from-white/40 to-transparent dark:from-white/5" />
        <div className="pointer-events-none absolute inset-x-4 bottom-4 rounded-full bg-background/80 px-3 py-1.5 text-center text-[11px] font-medium tracking-[0.08em] text-muted-foreground backdrop-blur">
          拖动调整位置，滚轮或滑杆缩放
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>缩放</span>
          <span>{transform.zoom.toFixed(2)}x</span>
        </div>
        <input
          type="range"
          min={String(LOGIN_PAGE_IMAGE_MIN_ZOOM)}
          max={String(LOGIN_PAGE_IMAGE_MAX_ZOOM)}
          step="0.01"
          value={transform.zoom}
          className="w-full accent-[#1456f0]"
          onChange={(event) => applyZoomAtPoint(Number(event.target.value), frameSize.width / 2, frameSize.height / 2)}
        />
      </div>

      <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>
          水平 {transform.positionX.toFixed(0)}% / 垂直 {transform.positionY.toFixed(0)}%
        </span>
        <Button
          type="button"
          variant="ghost"
          className="h-auto px-2 py-1 text-xs"
          onClick={() => commitTransform(LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM)}
        >
          <RotateCcw className="size-3.5" />
          重置视图
        </Button>
      </div>
    </div>
  );
}
