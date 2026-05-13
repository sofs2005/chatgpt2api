import webConfig from "@/constants/common-env";
import { getStoredSessionToken } from "@/store/auth";

const MANAGED_IMAGE_PREFIXES = ["/images/", "/image-thumbnails/"] as const;
const MAX_CACHED_AUTHENTICATED_IMAGE_ENTRIES = 320;
const MAX_CACHED_AUTHENTICATED_IMAGE_BYTES = 160 * 1024 * 1024;

type CachedAuthenticatedImage = {
  objectURL: string;
  byteSize: number;
  references: number;
  lastUsedAt: number;
};

export type RetainedAuthenticatedImage = {
  key: string;
  objectURL: string;
  byteSize: number;
};

const authenticatedImageCache = new Map<string, CachedAuthenticatedImage>();
const pendingAuthenticatedImageFetches = new Map<string, Promise<{ key: string; objectURL: string; byteSize: number }>>();
let authenticatedImageCacheBytes = 0;
let authenticatedImageCacheGeneration = 0;

function isAbsoluteURL(value: string) {
  return /^[a-z][a-z\d+.-]*:/i.test(value) || value.startsWith("//");
}

function browserBaseURL() {
  if (typeof window === "undefined") {
    return "http://localhost/";
  }
  return window.location.href;
}

function apiBaseURL() {
  const value = String(webConfig.apiUrl || "").trim();
  return value ? `${value.replace(/\/$/, "")}/` : "";
}

function trustedImageOrigins() {
  const origins = new Set<string>();
  if (typeof window !== "undefined") {
    origins.add(window.location.origin);
  }
  const apiBase = apiBaseURL();
  if (apiBase) {
    try {
      origins.add(new URL(apiBase).origin);
    } catch {
      // Ignore invalid runtime config and fall back to current-origin requests.
    }
  }
  return origins;
}

function isManagedImagePath(pathname: string) {
  return MANAGED_IMAGE_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}

function normalizeManagedCachePath(value: string) {
  return value.replace(/\\/g, "/").replace(/^\/+/, "");
}

function decodedPathSegment(value: string) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function managedImageSourcePathFromURL(value: string) {
  try {
    const pathname = new URL(value).pathname;
    if (pathname.startsWith("/images/")) {
      return normalizeManagedCachePath(decodedPathSegment(pathname.slice("/images/".length)));
    }
    if (pathname.startsWith("/image-thumbnails/")) {
      const thumbnailPath = decodedPathSegment(pathname.slice("/image-thumbnails/".length));
      return normalizeManagedCachePath(thumbnailPath.replace(/\.jpg$/i, ""));
    }
  } catch {
    // Ignore invalid cache keys; they cannot match managed image paths.
  }
  return "";
}

function touchCachedAuthenticatedImage(entry: CachedAuthenticatedImage) {
  entry.lastUsedAt = Date.now();
}

function retainAuthenticatedImageCacheEntry(key: string, entry: CachedAuthenticatedImage): RetainedAuthenticatedImage {
  touchCachedAuthenticatedImage(entry);
  entry.references += 1;
  return { key, objectURL: entry.objectURL, byteSize: entry.byteSize };
}

function trimAuthenticatedImageCache() {
  while (
    authenticatedImageCache.size > MAX_CACHED_AUTHENTICATED_IMAGE_ENTRIES ||
    authenticatedImageCacheBytes > MAX_CACHED_AUTHENTICATED_IMAGE_BYTES
  ) {
    let evictableKey = "";
    let evictableEntry: CachedAuthenticatedImage | null = null;
    for (const [key, entry] of authenticatedImageCache) {
      if (entry.references > 0) {
        continue;
      }
      if (!evictableEntry || entry.lastUsedAt < evictableEntry.lastUsedAt) {
        evictableKey = key;
        evictableEntry = entry;
      }
    }
    if (!evictableEntry) {
      return;
    }
    URL.revokeObjectURL(evictableEntry.objectURL);
    authenticatedImageCacheBytes -= evictableEntry.byteSize;
    authenticatedImageCache.delete(evictableKey);
  }
}

function storeAuthenticatedImageCacheEntry(key: string, objectURL: string, byteSize: number) {
  const existing = authenticatedImageCache.get(key);
  if (existing) {
    URL.revokeObjectURL(existing.objectURL);
    authenticatedImageCacheBytes -= existing.byteSize;
  }
  authenticatedImageCache.set(key, {
    objectURL,
    byteSize,
    references: existing?.references ?? 0,
    lastUsedAt: Date.now(),
  });
  authenticatedImageCacheBytes += byteSize;
  trimAuthenticatedImageCache();
}

export function resolveImageRequestURL(src: string) {
  const value = String(src || "").trim();
  if (!value) {
    return "";
  }

  const browserBase = browserBaseURL();
  const apiBase = apiBaseURL();
  if (!isAbsoluteURL(value) && value.startsWith("/") && apiBase) {
    const relativeCandidate = new URL(value, apiBase);
    if (isManagedImagePath(relativeCandidate.pathname)) {
      return relativeCandidate.toString();
    }
  }

  const candidate = new URL(value, browserBase);
  if (apiBase && isManagedImagePath(candidate.pathname)) {
    return new URL(`${candidate.pathname}${candidate.search}`, apiBase).toString();
  }

  return candidate.toString();
}

export function isManagedImageURL(src: string) {
  try {
    return isManagedImagePath(new URL(resolveImageRequestURL(src)).pathname);
  } catch {
    return false;
  }
}

function canAttachStoredSessionToken(src: string) {
  try {
    const url = new URL(resolveImageRequestURL(src));
    return isManagedImagePath(url.pathname) && trustedImageOrigins().has(url.origin);
  } catch {
    return false;
  }
}

export function shouldUseAuthenticatedImageFallback(src: string) {
  const value = String(src || "").trim();
  return Boolean(value) && !value.startsWith("data:") && !value.startsWith("blob:") && isManagedImageURL(value);
}

export async function fetchAuthenticatedImageBlob(src: string, signal?: AbortSignal) {
  const requestURL = resolveImageRequestURL(src);
  const headers: Record<string, string> = {};
  const canAttachToken = canAttachStoredSessionToken(src);
  if (canAttachToken) {
    const token = await getStoredSessionToken();
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
  }
  const managedImage = isManagedImageURL(src);

  const response = await fetch(requestURL, {
    headers,
    signal,
    credentials: headers.Authorization ? "omit" : managedImage ? "include" : "same-origin",
  });
  if (!response.ok) {
    throw new Error(`读取图片失败 (${response.status})`);
  }
  return response.blob();
}

export function retainCachedAuthenticatedImage(src: string): RetainedAuthenticatedImage | null {
  const key = resolveImageRequestURL(src);
  const entry = authenticatedImageCache.get(key);
  return entry ? retainAuthenticatedImageCacheEntry(key, entry) : null;
}

export async function fetchCachedAuthenticatedImage(src: string): Promise<RetainedAuthenticatedImage> {
  const key = resolveImageRequestURL(src);
  const cached = authenticatedImageCache.get(key);
  if (cached) {
    return retainAuthenticatedImageCacheEntry(key, cached);
  }

  const generation = authenticatedImageCacheGeneration;
  let pending = pendingAuthenticatedImageFetches.get(key);
  if (!pending) {
    pending = fetchAuthenticatedImageBlob(src)
      .then((blob) => {
        const objectURL = URL.createObjectURL(blob);
        if (generation !== authenticatedImageCacheGeneration) {
          URL.revokeObjectURL(objectURL);
          throw new Error("图片缓存已重置");
        }
        storeAuthenticatedImageCacheEntry(key, objectURL, blob.size);
        return { key, objectURL, byteSize: blob.size };
      })
      .finally(() => {
        pendingAuthenticatedImageFetches.delete(key);
      });
    pendingAuthenticatedImageFetches.set(key, pending);
  }

  await pending;
  const entry = authenticatedImageCache.get(key);
  if (!entry) {
    throw new Error("图片缓存不可用");
  }
  return retainAuthenticatedImageCacheEntry(key, entry);
}

export function releaseCachedAuthenticatedImage(key: string) {
  const entry = authenticatedImageCache.get(key);
  if (!entry) {
    return;
  }
  entry.references = Math.max(0, entry.references - 1);
  touchCachedAuthenticatedImage(entry);
  trimAuthenticatedImageCache();
}

export function getCachedAuthenticatedImageByteSize(src: string) {
  try {
    const entry = authenticatedImageCache.get(resolveImageRequestURL(src));
    if (!entry) {
      return 0;
    }
    touchCachedAuthenticatedImage(entry);
    return entry.byteSize;
  } catch {
    return 0;
  }
}

export function invalidateAuthenticatedImageCacheForPaths(paths: string[]) {
  const pathSet = new Set(paths.map(normalizeManagedCachePath));
  for (const [key, entry] of authenticatedImageCache) {
    const sourcePath = managedImageSourcePathFromURL(key);
    if (sourcePath && pathSet.has(sourcePath)) {
      URL.revokeObjectURL(entry.objectURL);
      authenticatedImageCacheBytes -= entry.byteSize;
      authenticatedImageCache.delete(key);
    }
  }
}

export function clearAuthenticatedImageCache() {
  authenticatedImageCacheGeneration += 1;
  pendingAuthenticatedImageFetches.clear();
  for (const entry of authenticatedImageCache.values()) {
    URL.revokeObjectURL(entry.objectURL);
  }
  authenticatedImageCache.clear();
  authenticatedImageCacheBytes = 0;
}
