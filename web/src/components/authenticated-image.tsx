"use client";

import { useEffect, useMemo, useState, type ImgHTMLAttributes } from "react";

import { fetchAuthenticatedImageBlob, resolveImageRequestURL, shouldUseAuthenticatedImageFallback } from "@/lib/authenticated-image";

type AuthenticatedImageProps = Omit<ImgHTMLAttributes<HTMLImageElement>, "src"> & {
  src: string;
};

export function AuthenticatedImage({ src, ...props }: AuthenticatedImageProps) {
  const [objectSrc, setObjectSrc] = useState("");
  const [fallbackToDirectSrc, setFallbackToDirectSrc] = useState(false);
  const directSrc = useMemo(() => {
    if (!src) {
      return "";
    }
    try {
      return resolveImageRequestURL(src);
    } catch {
      return src;
    }
  }, [src]);
  const shouldFetchWithAuth = useMemo(() => shouldUseAuthenticatedImageFallback(src), [src]);

  useEffect(() => {
    setObjectSrc("");
    setFallbackToDirectSrc(false);
    if (!shouldFetchWithAuth) {
      return;
    }

    const controller = new AbortController();
    let active = true;
    let objectURL = "";

    void fetchAuthenticatedImageBlob(src, controller.signal)
      .then((blob) => {
        if (!active) {
          return;
        }
        objectURL = URL.createObjectURL(blob);
        setObjectSrc(objectURL);
      })
      .catch(() => {
        if (active && !controller.signal.aborted) {
          setFallbackToDirectSrc(true);
        }
      });

    return () => {
      active = false;
      controller.abort();
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [shouldFetchWithAuth, src]);

  const displaySrc = shouldFetchWithAuth ? objectSrc || (fallbackToDirectSrc ? directSrc : "") : directSrc;

  return <img {...props} src={displaySrc || undefined} />;
}
