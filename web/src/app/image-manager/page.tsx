"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy, Download, Eye, Globe2, ImageIcon, LoaderCircle, Lock, MoreHorizontal, RefreshCw, Search, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { DateRangeFilter } from "@/components/date-range-filter";
import { ImageLightbox } from "@/components/image-lightbox";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  deleteManagedImages,
  fetchManagedImages,
  updateManagedImageVisibility,
  type ImageVisibility,
  type ManagedImage,
} from "@/lib/api";
import {
  clearImageManagerCache,
  getImageManagerCache,
  imageManagerCacheKey,
  isFreshImageManagerCache,
  removeCachedManagedImages,
  updateImageManagerCache,
  type ImageGalleryView,
} from "@/lib/image-manager-cache";
import { formatImageFileSize } from "@/lib/image-size";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { hasAPIPermission, type StoredAuthSession } from "@/store/auth";

function getManagedImageFormatLabel(item: ManagedImage) {
  const normalized = (item.name || item.url).split("?")[0]?.match(/\.([a-z0-9]+)$/i)?.[1] || "image";
  const format = normalized.toLowerCase() === "jpeg" ? "jpg" : normalized.toLowerCase();
  return `IMAGE ${format.toUpperCase()}`;
}

function managedImageKey(item: ManagedImage) {
  return item.path;
}

function buildManagedImageDownloadName(item: ManagedImage, index: number) {
  const sourceName = item.name || item.url.split("?")[0]?.split("/").filter(Boolean).pop();
  if (sourceName) {
    return sourceName;
  }
  return `managed-image-${String(index + 1).padStart(2, "0")}.png`;
}

async function downloadManagedImage(item: ManagedImage, index: number) {
  let href = item.url;
  let objectUrl = "";

  try {
    const response = await fetch(item.url);
    if (response.ok) {
      const blob = await response.blob();
      objectUrl = URL.createObjectURL(blob);
      href = objectUrl;
    }
  } catch {
    href = item.url;
  }

  const link = document.createElement("a");
  link.href = href;
  link.download = buildManagedImageDownloadName(item, index);
  document.body.appendChild(link);
  link.click();
  link.remove();

  if (objectUrl) {
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
  }
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function isRequestCanceled(error: unknown) {
  return error instanceof Error && error.message === "canceled";
}

type DeleteImageTarget = {
  paths: string[];
};

type ImageVisibilityFilter = "all" | ImageVisibility;
type ImageFormatFilter = "all" | "png" | "jpg" | "webp" | "gif" | "other";
type ImageOrientationFilter = "all" | "landscape" | "portrait" | "square" | "unknown";

function imageManagerCacheScope(session: StoredAuthSession) {
  return [session.provider || "local", session.role, session.subjectId || session.key].join(":");
}

function getManagedImageFormat(item: ManagedImage) {
  const extension = (item.name || item.url || item.path).split("?")[0]?.match(/\.([a-z0-9]+)$/i)?.[1]?.toLowerCase();
  if (!extension) {
    return "other";
  }
  if (extension === "jpeg") {
    return "jpg";
  }
  return ["png", "jpg", "webp", "gif"].includes(extension) ? extension : "other";
}

function imageOwnerLabel(item: ManagedImage) {
  return item.owner_name?.trim() || "未知用户";
}

function getManagedImageOrientation(item: ManagedImage): ImageOrientationFilter {
  if (!item.width || !item.height) {
    return "unknown";
  }
  if (item.width === item.height) {
    return "square";
  }
  return item.width > item.height ? "landscape" : "portrait";
}

function matchesManagedImageKeyword(item: ManagedImage, keyword: string) {
  const normalizedKeyword = keyword.trim().toLowerCase();
  if (!normalizedKeyword) {
    return true;
  }
  return [
    item.name,
    item.path,
    item.url,
    item.owner_name,
    item.owner_id,
    item.created_at,
    item.date,
  ].some((value) => String(value || "").toLowerCase().includes(normalizedKeyword));
}

function imageVisibilityLabel(visibility: ImageVisibility) {
  return visibility === "public" ? "已公开" : "私有";
}

function imageVisibilityPillClass(visibility: ImageVisibility) {
  return visibility === "public"
    ? "bg-[#e8f2ff] text-[#1456f0] ring-1 ring-[#bfdbfe]"
    : "bg-[#181e25]/82 text-white ring-1 ring-white/20";
}

function imageVisibilityActionClass(visibility: ImageVisibility) {
  return visibility === "public"
    ? "bg-white/95 text-[#1456f0] hover:bg-[#e8f2ff]"
    : "bg-white/95 text-stone-800 hover:bg-stone-100";
}

function blurFocusedElementInContainer(container: HTMLElement) {
  const activeElement = document.activeElement;
  if (activeElement instanceof HTMLElement && container.contains(activeElement)) {
    activeElement.blur();
  }
}

const IMAGE_MASONRY_BREAKPOINTS = [
  { minWidth: 1280, columns: 4 },
  { minWidth: 1024, columns: 3 },
  { minWidth: 640, columns: 2 },
] as const;
const IMAGE_MANAGER_BATCH_SIZE = 40;
const IMAGE_MANAGER_LOAD_MORE_DELAY_MS = 220;
const AUTO_REFRESH_INTERVAL_OPTIONS = [60, 30, 10, 5] as const;
const AUTO_REFRESH_DISABLED_VALUE = "off";

type ImageAutoRefreshInterval = (typeof AUTO_REFRESH_INTERVAL_OPTIONS)[number] | typeof AUTO_REFRESH_DISABLED_VALUE;
type EnabledImageAutoRefreshInterval = Exclude<ImageAutoRefreshInterval, typeof AUTO_REFRESH_DISABLED_VALUE>;

function getImageMasonryColumnCount() {
  if (typeof window === "undefined") {
    return 1;
  }

  return IMAGE_MASONRY_BREAKPOINTS.find(({ minWidth }) =>
    window.matchMedia(`(min-width: ${minWidth}px)`).matches,
  )?.columns ?? 1;
}

function useOrderedImageMasonryColumns(items: ManagedImage[]) {
  const [columnCount, setColumnCount] = useState(getImageMasonryColumnCount);

  useEffect(() => {
    const updateColumnCount = () => setColumnCount(getImageMasonryColumnCount());
    const mediaQueries = IMAGE_MASONRY_BREAKPOINTS.map(({ minWidth }) =>
      window.matchMedia(`(min-width: ${minWidth}px)`),
    );

    updateColumnCount();
    mediaQueries.forEach((query) => query.addEventListener("change", updateColumnCount));
    return () => mediaQueries.forEach((query) => query.removeEventListener("change", updateColumnCount));
  }, []);

  return useMemo(() => {
    const columns = Array.from({ length: columnCount }, () => [] as Array<{ item: ManagedImage; index: number }>);
    items.forEach((item, index) => {
      columns[index % columnCount].push({ item, index });
    });
    return columns;
  }, [columnCount, items]);
}

function ImageManagerContent({
  cacheScope,
  canDeleteImages,
  isAdmin,
}: {
  cacheScope: string;
  canDeleteImages: boolean;
  isAdmin: boolean;
}) {
  const activeLoadRef = useRef<AbortController | null>(null);
  const autoRefreshAbortRef = useRef<AbortController | null>(null);
  const loadMoreTargetRef = useRef<HTMLDivElement | null>(null);
  const loadMoreTimerRef = useRef<number | null>(null);
  const [galleryView, setGalleryView] = useState<ImageGalleryView>("mine");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const currentCacheKey = imageManagerCacheKey(cacheScope, galleryView, startDate, endDate);
  const initialCache = getImageManagerCache(currentCacheKey);
  const [items, setItems] = useState<ManagedImage[]>(() => initialCache?.items ?? []);
  const [selectedImageIds, setSelectedImageIds] = useState<Record<string, boolean>>({});
  const [downloadingKey, setDownloadingKey] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteImageTarget | null>(null);
  const [isDeleting, setIsDeleting] = useState(false);
  const [visibilityMutatingPath, setVisibilityMutatingPath] = useState<string | null>(null);
  const [focusedImagePath, setFocusedImagePath] = useState<string | null>(null);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(() => !initialCache);
  const [loadError, setLoadError] = useState("");
  const [isAutoRefreshing, setIsAutoRefreshing] = useState(false);
  const [isImageActionsOpen, setIsImageActionsOpen] = useState(false);
  const [autoRefreshInterval, setAutoRefreshInterval] = useState<ImageAutoRefreshInterval>(30);
  const [visibleItemLimit, setVisibleItemLimit] = useState(IMAGE_MANAGER_BATCH_SIZE);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [searchKeyword, setSearchKeyword] = useState("");
  const [visibilityFilter, setVisibilityFilter] = useState<ImageVisibilityFilter>("all");
  const [formatFilter, setFormatFilter] = useState<ImageFormatFilter>("all");
  const [orientationFilter, setOrientationFilter] = useState<ImageOrientationFilter>("all");
  const filteredItems = useMemo(
    () =>
      items.filter((item) => {
        if (!matchesManagedImageKeyword(item, searchKeyword)) {
          return false;
        }
        if (visibilityFilter !== "all" && item.visibility !== visibilityFilter) {
          return false;
        }
        if (formatFilter !== "all" && getManagedImageFormat(item) !== formatFilter) {
          return false;
        }
        if (orientationFilter !== "all" && getManagedImageOrientation(item) !== orientationFilter) {
          return false;
        }
        return true;
      }),
    [formatFilter, items, orientationFilter, searchKeyword, visibilityFilter],
  );
  const hasLocalFilters = searchKeyword.trim() !== "" || visibilityFilter !== "all" || formatFilter !== "all" || orientationFilter !== "all";
  const hasActiveFilters = hasLocalFilters || startDate !== "" || endDate !== "";
  const visibleItems = useMemo(
    () => filteredItems.slice(0, visibleItemLimit),
    [filteredItems, visibleItemLimit],
  );
  const hasMoreFilteredItems = visibleItems.length < filteredItems.length;
  const lightboxImages = useMemo(
    () =>
      filteredItems.map((item) => ({
        id: item.name,
        src: item.url,
        sizeLabel: formatImageFileSize(item.size),
        dimensions: item.width && item.height ? `${item.width} x ${item.height}` : undefined,
      })),
    [filteredItems],
  );
  const selectedItems = useMemo(
    () => filteredItems.filter((item) => selectedImageIds[managedImageKey(item)]),
    [filteredItems, selectedImageIds],
  );
  const selectedPrivateItems = useMemo(
    () => selectedItems.filter((item) => item.visibility !== "public"),
    [selectedItems],
  );
  const selectedPublicItems = useMemo(
    () => selectedItems.filter((item) => item.visibility === "public"),
    [selectedItems],
  );
  const selectedCount = selectedItems.length;
  const allSelected = filteredItems.length > 0 && selectedCount === filteredItems.length;
  const isMutatingImages = downloadingKey !== null || isDeleting || visibilityMutatingPath !== null;
  const imageColumns = useOrderedImageMasonryColumns(visibleItems);
  const showImageLoadingState = isLoading && items.length === 0;
  const showImageErrorState = !isLoading && loadError !== "" && items.length === 0;
  const showImageEmptyState = !isLoading && loadError === "" && items.length === 0;
  const showImageFilteredEmptyState = !isLoading && loadError === "" && items.length > 0 && filteredItems.length === 0;

  const loadImages = useCallback(async ({ force = false }: { force?: boolean } = {}) => {
    const cached = getImageManagerCache(currentCacheKey);
    if (!force && cached) {
      setItems(cached.items);
      setSelectedImageIds({});
      setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
      setLoadError("");
      if (isFreshImageManagerCache(cached)) {
        setIsLoading(false);
        return;
      }
    }

    activeLoadRef.current?.abort();
    const controller = new AbortController();
    activeLoadRef.current = controller;
    setIsLoading(true);
    setLoadError("");
    try {
      const data = await fetchManagedImages(
        { scope: galleryView, start_date: startDate, end_date: endDate },
        { signal: controller.signal },
      );
      updateImageManagerCache(currentCacheKey, data.items);
      setItems(data.items);
      setSelectedImageIds({});
      setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
    } catch (error) {
      if (controller.signal.aborted || isRequestCanceled(error)) {
        return;
      }
      const message = error instanceof Error ? error.message : "加载图片失败";
      if (force || !cached) {
        setLoadError(message);
        toast.error(message);
      }
    } finally {
      if (activeLoadRef.current === controller) {
        activeLoadRef.current = null;
        setIsLoading(false);
      }
    }
  }, [currentCacheKey, endDate, galleryView, startDate]);

  const refreshNewImages = useCallback(async () => {
    if (isLoading || isMutatingImages || autoRefreshAbortRef.current) {
      return;
    }

    const controller = new AbortController();
    autoRefreshAbortRef.current = controller;
    setIsAutoRefreshing(true);
    try {
      const data = await fetchManagedImages(
        { scope: galleryView, start_date: startDate, end_date: endDate },
        { signal: controller.signal },
      );
      const incomingByPath = new Map(data.items.map((item) => [item.path, item]));
      const incomingPathSet = new Set(incomingByPath.keys());
      const knownPaths = new Set(items.map((item) => item.path));
      const incomingNewItems = data.items.filter((item) => !knownPaths.has(item.path));
      const hasRemovedItems = items.some((item) => !incomingPathSet.has(item.path));
      const hasUpdatedItems = items.some((item) => {
        const incoming = incomingByPath.get(item.path);
        return incoming ? JSON.stringify(incoming) !== JSON.stringify(item) : false;
      });
      if (incomingNewItems.length === 0 && !hasRemovedItems && !hasUpdatedItems) {
        return;
      }
      setItems((current) => {
        const currentPaths = new Set(current.map((item) => item.path));
        const newItems = data.items.filter((item) => !currentPaths.has(item.path));
        const existingItems = current.flatMap((item) => {
          const incoming = incomingByPath.get(item.path);
          return incoming ? [{ ...item, ...incoming }] : [];
        });
        const next = [...newItems, ...existingItems];
        if (next.length === current.length && newItems.length === 0 && !hasUpdatedItems) {
          return current;
        }
        updateImageManagerCache(currentCacheKey, next);
        return next;
      });
      if (hasRemovedItems) {
        setSelectedImageIds((current) => {
          const next = { ...current };
          Object.keys(next).forEach((path) => {
            if (!incomingPathSet.has(path)) {
              delete next[path];
            }
          });
          return next;
        });
      }
      setVisibleItemLimit((current) => current + incomingNewItems.length);
    } catch (error) {
      if (controller.signal.aborted || isRequestCanceled(error)) {
        return;
      }
    } finally {
      if (autoRefreshAbortRef.current === controller) {
        autoRefreshAbortRef.current = null;
      }
      setIsAutoRefreshing(false);
    }
  }, [currentCacheKey, endDate, galleryView, isLoading, isMutatingImages, items, startDate]);

  const scheduleLoadMoreImages = useCallback(() => {
    if (isLoadingMore || visibleItemLimit >= filteredItems.length) {
      return;
    }
    if (loadMoreTimerRef.current !== null) {
      return;
    }

    setIsLoadingMore(true);
    loadMoreTimerRef.current = window.setTimeout(() => {
      setVisibleItemLimit((current) => Math.min(current + IMAGE_MANAGER_BATCH_SIZE, filteredItems.length));
      setIsLoadingMore(false);
      loadMoreTimerRef.current = null;
    }, IMAGE_MANAGER_LOAD_MORE_DELAY_MS);
  }, [filteredItems.length, isLoadingMore, visibleItemLimit]);

  const handleGalleryViewChange = (view: ImageGalleryView) => {
    if (view === galleryView) {
      return;
    }
    setGalleryView(view);
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
    setLoadError("");
  };

  const updateSearchKeyword = (value: string) => {
    setSearchKeyword(value);
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
  };

  const updateVisibilityFilter = (value: ImageVisibilityFilter) => {
    setVisibilityFilter(value);
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
  };

  const updateFormatFilter = (value: ImageFormatFilter) => {
    setFormatFilter(value);
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
  };

  const updateOrientationFilter = (value: ImageOrientationFilter) => {
    setOrientationFilter(value);
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
  };

  const clearImageFilters = () => {
    setStartDate("");
    setEndDate("");
    setSearchKeyword("");
    setVisibilityFilter("all");
    setFormatFilter("all");
    setOrientationFilter("all");
    setSelectedImageIds({});
    setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
  };

  const updateAutoRefreshInterval = (value: string) => {
    if (value === AUTO_REFRESH_DISABLED_VALUE) {
      setAutoRefreshInterval(AUTO_REFRESH_DISABLED_VALUE);
      return;
    }
    const interval = Number(value);
    if (AUTO_REFRESH_INTERVAL_OPTIONS.includes(interval as EnabledImageAutoRefreshInterval)) {
      setAutoRefreshInterval(interval as EnabledImageAutoRefreshInterval);
    }
  };

  const toggleImageSelection = (item: ManagedImage) => {
    const key = managedImageKey(item);
    setSelectedImageIds((current) => ({
      ...current,
      [key]: !current[key],
    }));
  };

  const toggleAllImages = () => {
    if (allSelected) {
      setSelectedImageIds({});
      return;
    }

    setSelectedImageIds(
      Object.fromEntries(filteredItems.map((item) => [managedImageKey(item), true])),
    );
  };

  const downloadItems = async (key: string, downloadItems: ManagedImage[]) => {
    if (downloadItems.length === 0 || downloadingKey) {
      return;
    }

    setDownloadingKey(key);
    try {
      for (let index = 0; index < downloadItems.length; index += 1) {
        const item = downloadItems[index];
        await downloadManagedImage(item, items.indexOf(item));
        if (index < downloadItems.length - 1) {
          await sleep(120);
        }
      }
    } finally {
      setDownloadingKey(null);
    }
  };

  const openDeleteConfirm = (targetItems: ManagedImage[]) => {
    if (!canDeleteImages) {
      return;
    }
    const paths = Array.from(new Set(targetItems.map((item) => item.path)));
    if (paths.length === 0) {
      toast.error("没有可删除的图片");
      return;
    }
    setDeleteTarget({ paths });
  };

  const handleConfirmDelete = async () => {
    if (!canDeleteImages || !deleteTarget || isDeleting) {
      return;
    }

    const paths = deleteTarget.paths;
    const pathSet = new Set(paths);
    setIsDeleting(true);
    try {
      const data = await deleteManagedImages(paths);
      removeCachedManagedImages(paths);
      setItems((current) => current.filter((item) => !pathSet.has(item.path)));
      setSelectedImageIds((current) => {
        const next = { ...current };
        paths.forEach((path) => {
          delete next[path];
        });
        return next;
      });
      setLightboxOpen(false);
      setLightboxIndex(0);
      setDeleteTarget(null);
      toast.success(
        data.missing > 0
          ? `已删除 ${data.deleted} 张图片，${data.missing} 张已不存在`
          : `已删除 ${data.deleted} 张图片`,
      );
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除图片失败");
    } finally {
      setIsDeleting(false);
    }
  };

  const handleVisibilityChange = async (item: ManagedImage, visibility: ImageVisibility) => {
    if (galleryView !== "mine" || visibilityMutatingPath) {
      return;
    }
    const previousVisibility = item.visibility;
    if (previousVisibility === visibility) {
      return;
    }
    setVisibilityMutatingPath(item.path);
    try {
      const data = await updateManagedImageVisibility(item.path, visibility);
      const updated = {
        ...data.item,
        path: item.path,
        visibility: data.item.visibility || visibility,
      };
      clearImageManagerCache();
      setItems((current) => {
        const next = current.map((currentItem) =>
          currentItem.path === item.path
            ? {
                ...currentItem,
                ...updated,
              }
            : currentItem,
        );
        updateImageManagerCache(currentCacheKey, next);
        return next;
      });
      toast.success(visibility === "public" ? "已公开到公开图库" : "已取消公开");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新公开状态失败");
    } finally {
      setVisibilityMutatingPath(null);
    }
  };

  const handleBulkVisibilityChange = async (targetItems: ManagedImage[], visibility: ImageVisibility) => {
    if (galleryView !== "mine" || visibilityMutatingPath) {
      return;
    }
    const pendingItems = targetItems.filter((item) => item.visibility !== visibility);
    if (pendingItems.length === 0) {
      return;
    }

    setVisibilityMutatingPath(`bulk:${visibility}`);
    try {
      const results = await Promise.allSettled(
        pendingItems.map(async (item) => {
          const data = await updateManagedImageVisibility(item.path, visibility);
          return {
            ...data.item,
            path: item.path,
            visibility: data.item.visibility || visibility,
          };
        }),
      );
      const updates = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
      const failedCount = results.length - updates.length;

      if (updates.length > 0) {
        const updatesByPath = new Map(updates.map((item) => [item.path, item]));
        clearImageManagerCache();
        setItems((current) => {
          const next = current.map((currentItem) => {
            const updated = updatesByPath.get(currentItem.path);
            return updated ? { ...currentItem, ...updated } : currentItem;
          });
          updateImageManagerCache(currentCacheKey, next);
          return next;
        });
      }

      if (failedCount > 0) {
        toast.error(`已更新 ${updates.length} 张图片，${failedCount} 张失败`);
        return;
      }
      toast.success(visibility === "public" ? `已公开 ${updates.length} 张图片` : `已设为私有 ${updates.length} 张图片`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "批量更新公开状态失败");
    } finally {
      setVisibilityMutatingPath(null);
    }
  };

  useEffect(() => {
    void loadImages();
  }, [loadImages]);

  useEffect(() => {
    if (autoRefreshInterval === AUTO_REFRESH_DISABLED_VALUE) {
      autoRefreshAbortRef.current?.abort();
      setIsAutoRefreshing(false);
      return;
    }
    const timer = window.setInterval(() => {
      void refreshNewImages();
    }, autoRefreshInterval * 1000);
    return () => window.clearInterval(timer);
  }, [autoRefreshInterval, refreshNewImages]);

  useEffect(() => {
    autoRefreshAbortRef.current?.abort();
  }, [currentCacheKey]);

  useEffect(() => {
    if (!hasMoreFilteredItems) {
      return;
    }
    const target = loadMoreTargetRef.current;
    if (!target) {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          scheduleLoadMoreImages();
        }
      },
      { rootMargin: "520px 0px" },
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [hasMoreFilteredItems, scheduleLoadMoreImages]);

  useEffect(() => {
    return () => {
      activeLoadRef.current?.abort();
      autoRefreshAbortRef.current?.abort();
      if (loadMoreTimerRef.current !== null) {
        window.clearTimeout(loadMoreTimerRef.current);
      }
    };
  }, []);

  return (
    <section className="flex flex-col gap-5 pb-20 sm:pb-24">
      <PageHeader eyebrow="Images" title="图片库" />

      <div className="flex flex-col gap-4">
        <section className="grid gap-4 rounded-[18px] border border-border bg-background/80 p-3 shadow-[0_6px_20px_rgba(15,23,42,0.04)] sm:p-4 lg:grid-cols-[minmax(180px,220px)_minmax(0,1fr)] lg:items-start">
          <div className="flex min-w-0 flex-col gap-2">
            <div className="inline-flex w-full rounded-lg border border-border bg-muted/50 p-1">
              {[
                { value: "mine" as const, label: "个人图库", icon: ImageIcon },
                { value: "public" as const, label: "公开图库", icon: Globe2 },
              ].map((option) => {
                const Icon = option.icon;
                const active = galleryView === option.value;
                return (
                  <button
                    key={option.value}
                    type="button"
                    className={`inline-flex h-8 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-3 text-sm font-medium transition ${
                      active
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                    onClick={() => handleGalleryViewChange(option.value)}
                    aria-pressed={active}
                  >
                    <Icon className="size-4" />
                    {option.label}
                  </button>
                );
              })}
            </div>
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
              <ImageIcon className="size-4 shrink-0" />
              <span>{galleryView === "mine" ? "个人图库" : "公开图库"}</span>
              <span>{hasLocalFilters ? `显示 ${filteredItems.length} / ${items.length} 张` : `共 ${items.length} 张`}</span>
              {isAutoRefreshing ? (
                <span className="inline-flex items-center gap-1 text-[#1456f0]">
                  <LoaderCircle className="size-3 animate-spin" />
                  自动刷新中
                </span>
              ) : null}
            </div>
          </div>

          <div className="flex min-w-0 flex-col gap-2">
            <div className="flex items-center justify-between gap-2">
              <div className="text-sm font-medium text-foreground">筛选项</div>
              {hasActiveFilters ? (
                <button
                  type="button"
                  className="inline-flex h-7 shrink-0 items-center gap-1 rounded-full px-2 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground"
                  onClick={clearImageFilters}
                >
                  <X className="size-3.5" />
                  清空
                </button>
              ) : null}
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 xl:grid-cols-[240px_minmax(240px,1fr)_150px_140px_140px_140px]">
              <DateRangeFilter
                className="col-span-2 w-full xl:col-span-1 xl:w-[240px]"
                startDate={startDate}
                endDate={endDate}
                onChange={(start, end) => {
                  setStartDate(start);
                  setEndDate(end);
                  setSelectedImageIds({});
                  setVisibleItemLimit(IMAGE_MANAGER_BATCH_SIZE);
                }}
              />
              <div className="relative col-span-2 xl:col-span-1">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={searchKeyword}
                  onChange={(event) => updateSearchKeyword(event.target.value)}
                  placeholder="搜索文件、路径、作者、日期"
                  className="h-10 rounded-lg pr-9 pl-9"
                />
                {searchKeyword ? (
                  <button
                    type="button"
                    className="absolute top-1/2 right-2 inline-flex size-6 -translate-y-1/2 items-center justify-center rounded-full text-muted-foreground transition hover:bg-muted hover:text-foreground"
                    onClick={() => updateSearchKeyword("")}
                    aria-label="清空搜索"
                    title="清空搜索"
                  >
                    <X className="size-3.5" />
                  </button>
                ) : null}
              </div>
              <Select value={visibilityFilter} onValueChange={(value) => updateVisibilityFilter(value as ImageVisibilityFilter)}>
                <SelectTrigger className="h-10 min-w-0 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部状态</SelectItem>
                    <SelectItem value="public">已公开</SelectItem>
                    <SelectItem value="private">私有</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select value={formatFilter} onValueChange={(value) => updateFormatFilter(value as ImageFormatFilter)}>
                <SelectTrigger className="h-10 min-w-0 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部格式</SelectItem>
                    <SelectItem value="png">PNG</SelectItem>
                    <SelectItem value="jpg">JPG</SelectItem>
                    <SelectItem value="webp">WEBP</SelectItem>
                    <SelectItem value="gif">GIF</SelectItem>
                    <SelectItem value="other">其他</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select value={orientationFilter} onValueChange={(value) => updateOrientationFilter(value as ImageOrientationFilter)}>
                <SelectTrigger className="h-10 min-w-0 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部方向</SelectItem>
                    <SelectItem value="landscape">横图</SelectItem>
                    <SelectItem value="portrait">竖图</SelectItem>
                    <SelectItem value="square">方图</SelectItem>
                    <SelectItem value="unknown">未知尺寸</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select value={String(autoRefreshInterval)} onValueChange={updateAutoRefreshInterval}>
                <SelectTrigger className="h-10 min-w-0 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value={AUTO_REFRESH_DISABLED_VALUE}>不自动刷新</SelectItem>
                    <SelectItem value="60">60 秒刷新</SelectItem>
                    <SelectItem value="30">30 秒刷新</SelectItem>
                    <SelectItem value="10">10 秒刷新</SelectItem>
                    <SelectItem value="5">5 秒刷新</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          </div>

        </section>

        <Popover open={isImageActionsOpen} onOpenChange={setIsImageActionsOpen}>
          <div className="fixed right-4 bottom-[calc(env(safe-area-inset-bottom)+1rem)] z-40 sm:right-6 sm:bottom-6">
            <PopoverTrigger asChild>
              <Button
                type="button"
                className="h-12 rounded-full px-4 shadow-[0_18px_50px_-24px_rgba(15,23,42,0.65)]"
                aria-label="打开图片操作"
              >
                <MoreHorizontal className="size-5" />
                <span>操作</span>
                {selectedCount > 0 ? (
                  <span className="ml-0.5 inline-flex min-w-5 items-center justify-center rounded-full bg-white/20 px-1.5 text-xs font-semibold text-white">
                    {selectedCount}
                  </span>
                ) : null}
              </Button>
            </PopoverTrigger>
            <PopoverContent
              align="end"
              side="top"
              sideOffset={10}
              className="w-[min(calc(100vw-2rem),20rem)] p-2"
            >
              <div className="flex flex-col gap-1">
                <div className="px-2 py-1.5 text-xs font-medium text-muted-foreground">
                  {hasLocalFilters ? `显示 ${filteredItems.length} / ${items.length} 张` : `共 ${items.length} 张`}
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  className="h-10 justify-start rounded-lg px-3 text-sm"
                  disabled={filteredItems.length === 0 || isMutatingImages}
                  onClick={toggleAllImages}
                >
                  <Check className="size-4" />
                  {allSelected ? "取消全选" : "全选"}
                </Button>
                {galleryView === "mine" ? (
                  <>
                    <Button
                      type="button"
                      variant="ghost"
                      className="h-10 justify-start rounded-lg px-3 text-sm"
                      disabled={selectedPrivateItems.length === 0 || isMutatingImages}
                      onClick={() => void handleBulkVisibilityChange(selectedPrivateItems, "public")}
                    >
                      {visibilityMutatingPath === "bulk:public" ? (
                        <LoaderCircle className="size-4 animate-spin" />
                      ) : (
                        <Globe2 className="size-4" />
                      )}
                      公开已选 ({selectedPrivateItems.length})
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      className="h-10 justify-start rounded-lg px-3 text-sm"
                      disabled={selectedPublicItems.length === 0 || isMutatingImages}
                      onClick={() => void handleBulkVisibilityChange(selectedPublicItems, "private")}
                    >
                      {visibilityMutatingPath === "bulk:private" ? (
                        <LoaderCircle className="size-4 animate-spin" />
                      ) : (
                        <Lock className="size-4" />
                      )}
                      设为私有 ({selectedPublicItems.length})
                    </Button>
                  </>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  className="h-10 justify-start rounded-lg px-3 text-sm"
                  disabled={selectedCount === 0 || isMutatingImages}
                  onClick={() => void downloadItems("selected", selectedItems)}
                >
                  {downloadingKey === "selected" ? (
                    <LoaderCircle className="size-4 animate-spin" />
                  ) : (
                    <Download className="size-4" />
                  )}
                  下载已选 ({selectedCount})
                </Button>
                {canDeleteImages ? (
                  <Button
                    type="button"
                    variant="ghost"
                    className="h-10 justify-start rounded-lg px-3 text-sm text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                    disabled={selectedCount === 0 || isMutatingImages}
                    onClick={() => {
                      setIsImageActionsOpen(false);
                      openDeleteConfirm(selectedItems);
                    }}
                  >
                    <Trash2 className="size-4" />
                    删除已选 ({selectedCount})
                  </Button>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  className="h-10 justify-start rounded-lg px-3 text-sm"
                  disabled={filteredItems.length === 0 || isMutatingImages}
                  onClick={() => void downloadItems("all", filteredItems)}
                >
                  {downloadingKey === "all" ? (
                    <LoaderCircle className="size-4 animate-spin" />
                  ) : (
                    <Download className="size-4" />
                  )}
                  下载全部 ({filteredItems.length})
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  className="h-10 justify-start rounded-lg px-3 text-sm"
                  disabled={isLoading || isMutatingImages}
                  onClick={() => void loadImages({ force: true })}
                >
                  <RefreshCw className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
                  刷新
                </Button>
              </div>
            </PopoverContent>
          </div>
        </Popover>

        {showImageLoadingState ? (
          <Card className="overflow-hidden rounded-[20px]">
            <CardContent className="flex min-h-[280px] flex-col items-center justify-center gap-3 px-6 py-14 text-center">
              <div className="rounded-[16px] bg-[#edf4ff] p-4 text-[#1456f0] ring-1 ring-blue-100">
                <LoaderCircle className="size-7 animate-spin" />
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium text-foreground">正在加载图片</p>
              </div>
            </CardContent>
          </Card>
        ) : null}

        {showImageErrorState ? (
          <Card className="overflow-hidden rounded-[20px]">
            <CardContent className="flex min-h-[280px] flex-col items-center justify-center gap-3 px-6 py-14 text-center">
              <div className="rounded-[16px] bg-rose-50 p-4 text-rose-600 ring-1 ring-rose-100">
                <ImageIcon className="size-7" />
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium text-foreground">图片库加载失败</p>
                <p className="max-w-[32rem] text-sm leading-6 text-muted-foreground">{loadError}</p>
              </div>
              <Button variant="outline" className="h-9 rounded-lg px-3" onClick={() => void loadImages({ force: true })}>
                <RefreshCw className="size-4" />
                重试
              </Button>
            </CardContent>
          </Card>
        ) : null}

        {filteredItems.length > 0 ? (
          <div
            className="grid gap-3 sm:gap-4"
            style={{ gridTemplateColumns: `repeat(${imageColumns.length}, minmax(0, 1fr))` }}
          >
          {imageColumns.map((column, columnIndex) => (
            <div key={columnIndex} className="flex min-w-0 flex-col gap-3 sm:gap-4">
              {column.map(({ item, index }) => {
                const imageKey = managedImageKey(item);
                const selected = Boolean(selectedImageIds[imageKey]);
                const focused = focusedImagePath === imageKey;
                const dimensions = item.width && item.height ? `${item.width} x ${item.height}` : "";
                const sizeLabel = formatImageFileSize(item.size);
                const imageMeta = [dimensions, sizeLabel].filter(Boolean).join(" | ");
                const ownerLabel = imageOwnerLabel(item);
                const canUpdateVisibility = galleryView === "mine";
                const showVisibilityStatus = canUpdateVisibility || (isAdmin && galleryView === "public");
                return (
                  <figure
                    key={item.url}
                    className={`group relative w-full overflow-hidden rounded-[22px] bg-muted shadow-[0_0_15px_rgba(44,30,116,0.16)] ${selected ? "ring-2 ring-[#1456f0]/80 ring-offset-2" : ""}`}
                    style={{
                      contentVisibility: "auto",
                      containIntrinsicSize: item.width && item.height ? `${Math.min(360, item.width)}px ${Math.min(480, item.height)}px` : "320px 320px",
                    }}
                    onMouseLeave={(event) => blurFocusedElementInContainer(event.currentTarget)}
                    onBlurCapture={(event) => {
                      if (!event.currentTarget.contains(event.relatedTarget)) {
                        setFocusedImagePath((current) => (current === imageKey ? null : current));
                      }
                    }}
                  >
                    <button
                      type="button"
                      onClick={(event) => {
                        if (!window.matchMedia("(hover: hover)").matches) {
                          setFocusedImagePath(selected ? null : imageKey);
                        }
                        toggleImageSelection(item);
                        if (window.matchMedia("(hover: hover)").matches) {
                          event.currentTarget.blur();
                        }
                      }}
                      className="block w-full cursor-pointer overflow-hidden text-left"
                      onFocus={() => setFocusedImagePath(imageKey)}
                      aria-label={selected ? "取消选择图片" : "选择图片"}
                    >
                      <img
                        src={item.thumbnail_url || item.url}
                        alt={item.name}
                        width={item.width || undefined}
                        height={item.height || undefined}
                        loading="lazy"
                        decoding="async"
                        sizes="(min-width: 1280px) 25vw, (min-width: 1024px) 33vw, (min-width: 640px) 50vw, 100vw"
                        className="block h-auto w-full transition duration-200 group-hover:brightness-95"
                      />
                    </button>
                    <button
                      type="button"
                      onClick={(event) => {
                        if (!window.matchMedia("(hover: hover)").matches) {
                          setFocusedImagePath(selected ? null : imageKey);
                        }
                        toggleImageSelection(item);
                        if (window.matchMedia("(hover: hover)").matches) {
                          event.currentTarget.blur();
                        }
                      }}
                      className={`absolute top-2 left-2 z-10 inline-flex size-6 items-center justify-center rounded-full border transition duration-150 ${
                        selected
                          ? "border-[#1456f0] bg-[#1456f0] text-white opacity-100 shadow-sm"
                          : "pointer-events-none border-white/90 bg-black/20 text-transparent opacity-0 shadow-sm group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 hover:bg-black/30"
                      }`}
                      aria-label={selected ? "取消选择图片" : "选择图片"}
                    >
                      {selected ? <Check className="size-3.5" /> : null}
                    </button>
                    <div
                      className={`absolute top-2 right-2 z-10 flex items-center gap-1 transition duration-150 ${
                        focused
                          ? "pointer-events-auto opacity-100"
                          : "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100"
                      }`}
                    >
                      <button
                        type="button"
                        onClick={(event) => {
                          event.currentTarget.blur();
                          setLightboxIndex(index);
                          setLightboxOpen(true);
                        }}
                        className="inline-flex h-7 items-center gap-1 rounded-full bg-white/95 px-2 text-[11px] font-medium text-stone-800 shadow-sm transition hover:bg-white hover:text-stone-950"
                        aria-label="View Original"
                        title="View Original"
                      >
                        <Eye className="size-3" />
                        View Original
                      </button>
                      <button
                        type="button"
                        onClick={(event) => {
                          event.currentTarget.blur();
                          void navigator.clipboard.writeText(item.url);
                          toast.success("图片地址已复制");
                        }}
                        className="inline-flex size-7 items-center justify-center rounded-full bg-white/95 text-stone-800 shadow-sm transition hover:bg-white hover:text-stone-950"
                        aria-label="复制图片地址"
                        title="复制图片地址"
                      >
                        <Copy className="size-3.5" />
                      </button>
                      {canDeleteImages ? (
                        <button
                          type="button"
                          onClick={(event) => {
                            event.currentTarget.blur();
                            openDeleteConfirm([item]);
                          }}
                          disabled={isDeleting}
                          className="inline-flex size-7 items-center justify-center rounded-full bg-white/95 text-rose-600 shadow-sm transition hover:bg-rose-50 hover:text-rose-700 disabled:cursor-not-allowed disabled:opacity-60"
                          aria-label="删除图片"
                          title="删除图片"
                        >
                          {isDeleting && deleteTarget?.paths.includes(item.path) ? (
                            <LoaderCircle className="size-3.5 animate-spin" />
                          ) : (
                            <Trash2 className="size-3.5" />
                          )}
                        </button>
                      ) : null}
                    </div>
                    <div className="absolute right-2 bottom-2 left-2 z-10 flex items-center justify-between gap-2">
                      <div className="min-w-0 rounded-full bg-black/45 px-2 py-1 text-[11px] font-medium text-white shadow-sm backdrop-blur-sm">
                        <span className="block max-w-[12rem] truncate">{ownerLabel}</span>
                      </div>
                      {showVisibilityStatus ? (
                        <div className="flex shrink-0 items-center gap-1">
                          {canUpdateVisibility ? (
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                void handleVisibilityChange(item, item.visibility === "public" ? "private" : "public");
                              }}
                              disabled={visibilityMutatingPath !== null || isDeleting}
                              className={`inline-flex h-7 items-center gap-1.5 rounded-full px-2.5 text-[11px] font-medium shadow-sm transition disabled:cursor-not-allowed disabled:opacity-70 ${
                                focused ? "opacity-100" : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100"
                              } ${imageVisibilityActionClass(item.visibility)}`}
                            >
                              {visibilityMutatingPath === item.path ? (
                                <LoaderCircle className="size-3 animate-spin" />
                              ) : item.visibility === "public" ? (
                                <Lock className="size-3" />
                              ) : (
                                <Globe2 className="size-3" />
                              )}
                              {item.visibility === "public" ? "取消公开" : "公开"}
                            </button>
                          ) : null}
                          <div className={`pointer-events-none inline-flex h-7 items-center gap-1 rounded-full px-2 text-[11px] font-medium shadow-sm backdrop-blur-sm ${imageVisibilityPillClass(item.visibility)}`}>
                            {item.visibility === "public" ? <Globe2 className="size-3" /> : <Lock className="size-3" />}
                            {imageVisibilityLabel(item.visibility)}
                          </div>
                        </div>
                      ) : null}
                    </div>
                    <div
                      className={`pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/55 via-black/20 to-transparent px-2.5 pt-8 pb-10 transition duration-150 ${
                        focused ? "opacity-100" : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100"
                      }`}
                    >
                      <div className="text-left text-white drop-shadow-sm">
                        <div className="text-[10px] font-bold tracking-wide">{getManagedImageFormatLabel(item)}</div>
                        <div className="mt-0.5 truncate text-[11px] text-white/90">{item.created_at}</div>
                        <div className="mt-0.5 truncate text-[11px] text-white/90">作者：{ownerLabel}</div>
                        {imageMeta ? (
                          <div className="mt-0.5 truncate text-[11px] text-white/90">{imageMeta}</div>
                        ) : null}
                      </div>
                    </div>
                  </figure>
                );
              })}
            </div>
          ))}
          </div>
        ) : null}

        {hasMoreFilteredItems ? (
          <div ref={loadMoreTargetRef} className="flex min-h-16 items-center justify-center py-4 text-sm text-muted-foreground">
            <div className="inline-flex items-center gap-2 rounded-full border border-border bg-background px-4 py-2 shadow-sm">
              <LoaderCircle className={`size-4 text-[#1456f0] ${isLoadingMore ? "animate-spin" : ""}`} />
              {isLoadingMore
                ? "加载中..."
                : `下滑加载更多（${visibleItems.length} / ${filteredItems.length}）`}
            </div>
          </div>
        ) : filteredItems.length > IMAGE_MANAGER_BATCH_SIZE ? (
          <div className="flex justify-center py-4 text-xs text-muted-foreground">已显示全部图片</div>
        ) : null}

        {showImageEmptyState || showImageFilteredEmptyState ? (
          <Card className="overflow-hidden rounded-[20px]">
            <CardContent className="flex min-h-[320px] flex-col items-center justify-center gap-4 px-6 py-14 text-center">
              <div className="grid aspect-[4/3] w-[min(320px,72vw)] place-items-center rounded-[24px] border border-dashed border-border bg-muted/60 shadow-[0_0_15px_rgba(44,30,116,0.10)]">
                <div className="flex size-20 items-center justify-center rounded-[20px] bg-white text-[#1456f0] shadow-[0_8px_24px_rgba(24,40,72,0.07)]">
                  <ImageIcon className="size-9" />
                </div>
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium text-foreground">{showImageFilteredEmptyState ? "没有匹配的图片" : "暂无图片"}</p>
                <p className="max-w-[32rem] text-sm leading-6 text-muted-foreground">
                  {showImageFilteredEmptyState
                    ? "调整关键词、状态、格式或方向筛选后再试。"
                    : galleryView === "mine"
                      ? "图片生成成功后会自动进入个人图库。"
                      : "公开图库暂无公开图片。"}
                </p>
              </div>
            </CardContent>
          </Card>
        ) : null}
      </div>
      <ImageLightbox
        images={lightboxImages}
        currentIndex={lightboxIndex}
        open={lightboxOpen}
        onOpenChange={setLightboxOpen}
        onIndexChange={setLightboxIndex}
      />
      {canDeleteImages && deleteTarget ? (
        <Dialog open onOpenChange={(open) => (!open && !isDeleting ? setDeleteTarget(null) : null)}>
          <DialogContent showCloseButton={false} className="rounded-2xl p-6">
            <DialogHeader className="gap-2">
              <DialogTitle>删除图片</DialogTitle>
              <DialogDescription className="text-sm leading-6">
                确认删除 {deleteTarget.paths.length} 张图片吗？这会同时删除本地原图和缩略图，删除后无法恢复。
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                className="h-10 rounded-xl border-stone-200 bg-white px-5 text-stone-700 hover:bg-stone-50"
                onClick={() => setDeleteTarget(null)}
                disabled={isDeleting}
              >
                取消
              </Button>
              <Button
                type="button"
                className="h-10 rounded-xl bg-rose-600 px-5 text-white hover:bg-rose-700"
                onClick={() => void handleConfirmDelete()}
                disabled={isDeleting}
              >
                {isDeleting ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                确认删除
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </section>
  );
}

export default function ImageManagerPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/image-manager");
  if (isCheckingAuth || !session) {
    return <div className="flex min-h-[40vh] items-center justify-center"><LoaderCircle className="size-5 animate-spin text-stone-400" /></div>;
  }
  const canDeleteImages = hasAPIPermission(session, "DELETE", "/api/images");
  return <ImageManagerContent cacheScope={imageManagerCacheScope(session)} canDeleteImages={canDeleteImages} isAdmin={session.role === "admin"} />;
}
