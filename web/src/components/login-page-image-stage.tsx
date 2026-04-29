"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";

import { DEFAULT_LOGIN_PAGE_IMAGE, resolveLoginPageImageSrc } from "@/lib/app-meta";
import { cn } from "@/lib/utils";
import {
  getLoginPageImageLayout,
  LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM,
  normalizeLoginPageImageMode,
  normalizeLoginPageImageTransform,
  type LoginPageImageMode,
} from "@/lib/login-page-image-layout";

type LoginPageImageStageProps = {
  alt?: string;
  className?: string;
  fillParent?: boolean;
  frameClassName?: string;
  imageClassName?: string;
  mode?: LoginPageImageMode | string;
  positionX?: number;
  positionY?: number;
  src?: string;
  zoom?: number;
};

export function LoginPageImageStage({
  alt = "登录页展示图",
  className,
  fillParent = false,
  frameClassName,
  imageClassName,
  mode = "contain",
  positionX = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX,
  positionY = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY,
  src,
  zoom = LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
}: LoginPageImageStageProps) {
  const frameRef = useRef<HTMLDivElement | null>(null);
  const [fallbackActive, setFallbackActive] = useState(false);
  const [imageSize, setImageSize] = useState({ width: 0, height: 0 });
  const [frameSize, setFrameSize] = useState({ width: 0, height: 0 });
  const resolvedMode = normalizeLoginPageImageMode(mode);
  const resolvedSrc = resolveLoginPageImageSrc(src);
  const fallbackSrc = resolveLoginPageImageSrc(DEFAULT_LOGIN_PAGE_IMAGE);
  const currentSrc = fallbackActive ? fallbackSrc : resolvedSrc;
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
    <div
      className={cn(
        "flex w-full max-w-[30rem] items-center justify-center",
        fillParent ? "h-full max-w-none min-h-0" : undefined,
        className,
      )}
    >
      <div
        ref={frameRef}
        className={cn(
          "flex w-full items-center justify-center overflow-hidden rounded-[1.8rem]",
          fillParent ? "relative h-full w-full min-h-0 rounded-none" : "aspect-[16/10]",
          frameClassName,
        )}
      >
        <img
          src={currentSrc}
          alt={alt}
          className={cn("absolute top-0 left-0 max-w-none select-none", imageClassName)}
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
              setFallbackActive(true);
            }
          }}
          style={imageStyle}
        />
      </div>
    </div>
  );
}
