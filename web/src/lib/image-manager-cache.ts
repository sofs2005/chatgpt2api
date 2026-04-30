import type { ManagedImage } from "@/lib/api";

export type ImageGalleryView = "mine" | "public";

type ImageManagerCacheEntry = {
  items: ManagedImage[];
  updatedAt: number;
};

const IMAGE_MANAGER_CACHE_TTL_MS = 30 * 1000;
const imageManagerCache = new Map<string, ImageManagerCacheEntry>();

export function imageManagerCacheKey(cacheScope: string, view: ImageGalleryView, startDate: string, endDate: string) {
  return [cacheScope, view, startDate, endDate].join("|");
}

export function getImageManagerCache(cacheKey: string) {
  return imageManagerCache.get(cacheKey);
}

export function isFreshImageManagerCache(entry: ImageManagerCacheEntry) {
  return Date.now() - entry.updatedAt < IMAGE_MANAGER_CACHE_TTL_MS;
}

export function updateImageManagerCache(cacheKey: string, items: ManagedImage[]) {
  imageManagerCache.set(cacheKey, { items, updatedAt: Date.now() });
}

export function removeCachedManagedImages(paths: string[]) {
  const pathSet = new Set(paths);
  for (const [key, entry] of imageManagerCache) {
    const items = entry.items.filter((item) => !pathSet.has(item.path));
    if (items.length !== entry.items.length) {
      imageManagerCache.set(key, { items, updatedAt: Date.now() });
    }
  }
}

export function clearImageManagerCache() {
  imageManagerCache.clear();
}
