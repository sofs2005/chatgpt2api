import type {
  BananaPrompt,
  BananaPromptMode,
  PromptMarketLanguage,
  PromptMarketLocalization,
  PromptMarketSourceId,
} from "@/app/image/banana-prompts";
import { httpRequest } from "@/lib/request";

export type PromptFavorite = {
  id: string;
  prompt_id: string;
  source: PromptMarketSourceId;
  title: string;
  preview: string;
  reference_image_urls: string[];
  prompt: string;
  author: string;
  link?: string;
  mode: BananaPromptMode;
  category: string;
  sub_category?: string;
  created?: string;
  source_label: string;
  is_nsfw: boolean;
  localizations?: Partial<
    Record<
      PromptMarketLanguage,
      PromptMarketLocalization & {
        sub_category?: string;
      }
    >
  >;
  favorited_at: string;
  updated_at?: string;
};

export function promptFavoriteKey(prompt: Pick<BananaPrompt, "id" | "source">) {
  return `${prompt.source}:${prompt.id}`;
}

export function promptFavoriteRecordKey(favorite: Pick<PromptFavorite, "prompt_id" | "source">) {
  return `${favorite.source}:${favorite.prompt_id}`;
}

export function promptFavoriteToBananaPrompt(favorite: PromptFavorite): BananaPrompt {
  return {
    id: favorite.prompt_id,
    title: favorite.title,
    preview: favorite.preview,
    referenceImageUrls: favorite.reference_image_urls,
    prompt: favorite.prompt,
    author: favorite.author,
    link: favorite.link,
    mode: favorite.mode,
    category: favorite.category,
    subCategory: favorite.sub_category,
    created: favorite.created,
    source: favorite.source,
    sourceLabel: favorite.source_label,
    isNsfw: favorite.is_nsfw,
    localizations: normalizeFavoriteLocalizations(favorite.localizations),
  };
}

export function bananaPromptToFavoritePayload(prompt: BananaPrompt) {
  return {
    prompt_id: prompt.id,
    source: prompt.source,
    title: prompt.title,
    preview: prompt.preview,
    reference_image_urls: prompt.referenceImageUrls,
    prompt: prompt.prompt,
    author: prompt.author,
    link: prompt.link,
    mode: prompt.mode,
    category: prompt.category,
    sub_category: prompt.subCategory,
    created: prompt.created,
    source_label: prompt.sourceLabel,
    is_nsfw: prompt.isNsfw,
    localizations: prompt.localizations ? localizationsToPayload(prompt.localizations) : undefined,
  };
}

export async function fetchPromptFavorites(signal?: AbortSignal) {
  return httpRequest<{ items: PromptFavorite[] }>("/api/profile/prompt-favorites", { signal });
}

export async function createPromptFavorite(prompt: BananaPrompt) {
  return httpRequest<{ item: PromptFavorite; items: PromptFavorite[] }>("/api/profile/prompt-favorites", {
    method: "POST",
    body: bananaPromptToFavoritePayload(prompt),
  });
}

export async function deletePromptFavorite(favoriteId: string) {
  return httpRequest<{ items: PromptFavorite[] }>(`/api/profile/prompt-favorites/${encodeURIComponent(favoriteId)}`, {
    method: "DELETE",
  });
}

function normalizeFavoriteLocalizations(value: PromptFavorite["localizations"]): BananaPrompt["localizations"] {
  if (!value) {
    return undefined;
  }
  const localizations: BananaPrompt["localizations"] = {};
  for (const language of ["zh-CN", "en"] satisfies PromptMarketLanguage[]) {
    const item = value[language];
    if (!item) {
      continue;
    }
    localizations[language] = {
      title: item.title,
      prompt: item.prompt,
      category: item.category,
      subCategory: item.subCategory ?? item.sub_category,
    };
  }
  return Object.keys(localizations).length > 0 ? localizations : undefined;
}

function localizationsToPayload(localizations: NonNullable<BananaPrompt["localizations"]>) {
  const payload: Record<string, unknown> = {};
  for (const [language, item] of Object.entries(localizations)) {
    if (!item) {
      continue;
    }
    payload[language] = {
      title: item.title,
      prompt: item.prompt,
      category: item.category,
      sub_category: item.subCategory,
    };
  }
  return payload;
}
