export type BananaPromptMode = "generate" | "edit";
export type PromptMarketSourceId =
  | "banana-prompt-quicker"
  | "awesome-gpt-image-2-prompts"
  | "awesome-gpt-image-2"
  | "awesome-gpt-image-2-ch";
export type PromptMarketLanguage = "zh-CN" | "en";

export type PromptMarketLocalization = {
  title: string;
  prompt: string;
  category: string;
  subCategory?: string;
};

export type BananaPrompt = {
  id: string;
  title: string;
  preview: string;
  referenceImageUrls: string[];
  prompt: string;
  author: string;
  link?: string;
  mode: BananaPromptMode;
  category: string;
  subCategory?: string;
  created?: string;
  source: PromptMarketSourceId;
  sourceLabel: string;
  isNsfw: boolean;
  localizations?: Partial<Record<PromptMarketLanguage, PromptMarketLocalization>>;
};

export const BANANA_PROMPTS_SOURCE_URL = "https://github.com/glidea/banana-prompt-quicker";
export const BANANA_PROMPTS_URL =
  "https://raw.githubusercontent.com/glidea/banana-prompt-quicker/main/prompts.json";
// awesome-gpt-image-2-API-and-Prompts (replaces EvoLinkAI/awesome-gpt-image-2-prompts)
export const AWESOME_GPT_IMAGE_2_PROMPTS_SOURCE_URL =
  "https://github.com/sofs2005/awesome-gpt-image-2-API-and-Prompts";
export const AWESOME_GPT_IMAGE_2_PROMPTS_ZH_README_URL =
  "https://raw.githubusercontent.com/sofs2005/awesome-gpt-image-2-API-and-Prompts/main/README_zh-CN.md";
export const AWESOME_GPT_IMAGE_2_PROMPTS_EN_README_URL =
  "https://raw.githubusercontent.com/sofs2005/awesome-gpt-image-2-API-and-Prompts/main/README.md";
const AWESOME_GPT_IMAGE_2_PROMPTS_RAW_BASE_URL =
  "https://raw.githubusercontent.com/sofs2005/awesome-gpt-image-2-API-and-Prompts/main/";

// awesome-gpt-image-2 gallery content lives in the freestylefly upstream repo.
export const AWESOME_GPT_IMAGE_2_SOURCE_URL =
  "https://github.com/freestylefly/awesome-gpt-image-2";
const AWESOME_GPT_IMAGE_2_GALLERY_PART1_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/docs/gallery-part-1.md";
const AWESOME_GPT_IMAGE_2_GALLERY_PART2_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/docs/gallery-part-2.md";
const AWESOME_GPT_IMAGE_2_RAW_BASE_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/";

// The Chinese gallery view reuses the same upstream gallery data source.
export const AWESOME_GPT_IMAGE_2_CH_SOURCE_URL =
  "https://github.com/freestylefly/awesome-gpt-image-2";
const AWESOME_GPT_IMAGE_2_CH_GALLERY_PART1_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/docs/gallery-part-1.md";
const AWESOME_GPT_IMAGE_2_CH_GALLERY_PART2_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/docs/gallery-part-2.md";
const AWESOME_GPT_IMAGE_2_CH_RAW_BASE_URL =
  "https://raw.githubusercontent.com/freestylefly/awesome-gpt-image-2/main/";

export const PROMPT_MARKET_SOURCE_OPTIONS: {
  value: PromptMarketSourceId;
  label: string;
}[] = [
  {
    value: "banana-prompt-quicker",
    label: "banana-prompt-quicker",
  },
  {
    value: "awesome-gpt-image-2-prompts",
    label: "awesome-gpt-image-2-API-and-Prompts",
  },
  {
    value: "awesome-gpt-image-2",
    label: "awesome-gpt-image-2 (EN)",
  },
  {
    value: "awesome-gpt-image-2-ch",
    label: "awesome-gpt-image-2 (中文)",
  },
];

type BananaPromptSourceItem = {
  title?: unknown;
  preview?: unknown;
  reference_image_urls?: unknown;
  prompt?: unknown;
  author?: unknown;
  link?: unknown;
  mode?: unknown;
  category?: unknown;
  sub_category?: unknown;
  created?: unknown;
};

const MARKDOWN_CASE_HEADING_PATTERN =
  /^### Case\s+(\d+):\s+\[([^\]]+)](?:\(([^)]+)\))?(?:\s+\(by\s+\[([^\]]+)](?:\(([^)]+)\))?\))?/;
const MARKDOWN_IMAGE_PATTERN = /<img\s+[^>]*src=["']([^"']+)["'][^>]*>/i;
const MARKDOWN_PROMPT_PATTERN =
  /\*{2,}\s*(?:Prompt|提示词)\s*[:：]\s*\*{2,}\s*\n\s*```(?:\w+)?\n([\s\S]*?)\n```/i;
const IGNORED_MARKET_README_HEADINGS = new Set(["简介", "最新动态", "Menu", "致谢", "Star History"]);
const NSFW_TEXT_PATTERN =
  /\b(nsfw|nude|naked|lingerie|erotic|seductive|sexy|cleavage|underwear|panties|bra|bikini|ahegao|explicit|sensual|fetish|nipples?|genitals?|buttocks?|thong|topless)\b|裸|色情|情色|性感|诱惑|内衣|内裤|乳|胸|臀|私处|泳衣|比基尼|情趣|丁字裤|翻白眼|吐舌|妩媚|暧昧/i;

type AwesomePromptDraft = BananaPrompt & {
  language: PromptMarketLanguage;
  mergeKey: string;
};

function normalizePromptMode(value: unknown): BananaPromptMode {
  return value === "edit" ? "edit" : "generate";
}

function buildPromptId(item: BananaPromptSourceItem, index: number) {
  return [item.title, item.author, index]
    .map((part) => String(part || "").trim())
    .filter(Boolean)
    .join(":");
}

function normalizeReferenceImageUrls(value: unknown) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((url): url is string => typeof url === "string" && url.trim().length > 0);
}

function isNsfwPrompt(category: string, title: string, prompt: string) {
  return category === "NSFW" || NSFW_TEXT_PATTERN.test(`${category}\n${title}\n${prompt}`);
}

function normalizePrompt(item: BananaPromptSourceItem, index: number): BananaPrompt | null {
  if (
    typeof item.title !== "string" ||
    typeof item.preview !== "string" ||
    typeof item.prompt !== "string" ||
    typeof item.author !== "string"
  ) {
    return null;
  }

  const title = item.title.trim();
  const preview = item.preview.trim();
  const prompt = item.prompt.trim();
  const author = item.author.trim();
  const category =
    typeof item.category === "string" && item.category.trim() ? item.category.trim() : "未分类";
  if (!title || !preview || !prompt || !author) {
    return null;
  }

  return {
    id: `banana-prompt-quicker:${buildPromptId(item, index)}`,
    title,
    preview,
    prompt,
    author,
    referenceImageUrls: normalizeReferenceImageUrls(item.reference_image_urls),
    link: typeof item.link === "string" && item.link.trim() ? item.link.trim() : undefined,
    mode: normalizePromptMode(item.mode),
    category,
    subCategory: typeof item.sub_category === "string" && item.sub_category.trim() ? item.sub_category.trim() : undefined,
    created: typeof item.created === "string" && item.created.trim() ? item.created.trim() : undefined,
    source: "banana-prompt-quicker",
    sourceLabel: "banana-prompt-quicker",
    isNsfw: category === "NSFW",
  };
}

function normalizeMarkdownImageUrl(value: string) {
  const imageUrl = value.trim();
  if (!imageUrl) {
    return "";
  }
  if (/^https?:\/\/raw\.githubusercontent\.com\/EvoLinkAI\/awesome-gpt-image-2-API-and-Prompts\//i.test(imageUrl)) {
    return imageUrl.replace(
      /^https?:\/\/raw\.githubusercontent\.com\/EvoLinkAI\/awesome-gpt-image-2-API-and-Prompts\//i,
      "https://raw.githubusercontent.com/sofs2005/awesome-gpt-image-2-API-and-Prompts/",
    );
  }
  if (/^https?:\/\//i.test(imageUrl)) {
    return imageUrl;
  }
  return new URL(imageUrl.replace(/^\.\//, ""), AWESOME_GPT_IMAGE_2_PROMPTS_RAW_BASE_URL).toString();
}

function buildAwesomePromptMergeKey(link: string, preview: string) {
  return `${link.trim()}|${preview.trim()}`;
}

function cleanMarkdownHeading(value: string) {
  return value
    .replace(/^#+\s*/, "")
    .replace(/^[\p{Emoji_Presentation}\p{Extended_Pictographic}]\s*/u, "")
    .trim();
}

function normalizeAwesomePromptSection(
  section: string,
  category: string,
  language: PromptMarketLanguage,
  index: number,
): AwesomePromptDraft | null {
  const heading = section.match(MARKDOWN_CASE_HEADING_PATTERN);
  const image = section.match(MARKDOWN_IMAGE_PATTERN);
  const promptBlock = section.match(MARKDOWN_PROMPT_PATTERN);
  if (!heading || !image || !promptBlock) {
    return null;
  }

  const caseNumber = heading[1].trim();
  const title = heading[2].trim();
  const link = (heading[3] || "").trim();
  const author = (heading[4] || "Community").trim() || "Community";
  const preview = normalizeMarkdownImageUrl(image[1]);
  const prompt = promptBlock[1].trim();
  if (!caseNumber || !title || !preview || !prompt) {
    return null;
  }

  return {
    id: `awesome-gpt-image-2-prompts:${buildAwesomePromptMergeKey(link || `case-${caseNumber}`, preview)}`,
    title,
    preview,
    referenceImageUrls: [],
    prompt,
    author,
    link: link || undefined,
    mode: "generate",
    category,
    subCategory: `Case ${caseNumber}`,
    source: "awesome-gpt-image-2-prompts",
    sourceLabel: "awesome-gpt-image-2-prompts",
    isNsfw: isNsfwPrompt(category, title, prompt),
    language,
    mergeKey: buildAwesomePromptMergeKey(link || `case-${caseNumber}`, preview),
    localizations: {
      [language]: {
        title,
        prompt,
        category,
        subCategory: `Case ${caseNumber}`,
      },
    },
  };
}

function parseAwesomePrompts(markdown: string, language: PromptMarketLanguage) {
  const lines = markdown.split(/\r?\n/);
  const prompts: AwesomePromptDraft[] = [];
  let activeCategory = "未分类";

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (line.startsWith("## ")) {
      const heading = cleanMarkdownHeading(line);
      if (heading && !IGNORED_MARKET_README_HEADINGS.has(heading)) {
        activeCategory = heading;
      }
      continue;
    }
    if (!line.startsWith("### Case ")) {
      continue;
    }

    const sectionStart = index;
    let sectionEnd = lines.length;
    for (let nextIndex = index + 1; nextIndex < lines.length; nextIndex += 1) {
      if (lines[nextIndex].startsWith("### Case ") || lines[nextIndex].startsWith("## ")) {
        sectionEnd = nextIndex;
        break;
      }
    }

    const prompt = normalizeAwesomePromptSection(
      lines.slice(sectionStart, sectionEnd).join("\n"),
      activeCategory,
      language,
      prompts.length,
    );
    if (prompt) {
      prompts.push(prompt);
    }
    index = sectionEnd - 1;
  }

  return prompts;
}

function mergeAwesomePrompts(...groups: AwesomePromptDraft[][]) {
  const promptsByKey = new Map<string, AwesomePromptDraft>();

  groups.flat().forEach((prompt) => {
    const current = promptsByKey.get(prompt.mergeKey);
    if (!current) {
      promptsByKey.set(prompt.mergeKey, prompt);
      return;
    }

    current.localizations = {
      ...current.localizations,
      ...prompt.localizations,
    };
    current.isNsfw = current.isNsfw || prompt.isNsfw;
    if (current.language !== "zh-CN" && prompt.language === "zh-CN") {
      current.title = prompt.title;
      current.prompt = prompt.prompt;
      current.category = prompt.category;
      current.subCategory = prompt.subCategory;
      current.language = prompt.language;
    }
  });

  return [...promptsByKey.values()].map(({ language: _language, mergeKey: _mergeKey, ...prompt }) => prompt);
}

export async function fetchBananaPrompts(signal?: AbortSignal) {
  const response = await fetch(BANANA_PROMPTS_URL, {
    signal,
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(`读取提示词市场失败：${response.status}`);
  }

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error("提示词市场数据格式无效");
  }

  return data.flatMap((item, index) => {
    const prompt = normalizePrompt(item as BananaPromptSourceItem, index);
    return prompt ? [prompt] : [];
  });
}

export async function fetchAwesomeGptImage2Prompts(signal?: AbortSignal) {
  const [zhResponse, enResponse] = await Promise.all([
    fetch(AWESOME_GPT_IMAGE_2_PROMPTS_ZH_README_URL, {
      signal,
      headers: {
        Accept: "text/markdown,text/plain",
      },
    }),
    fetch(AWESOME_GPT_IMAGE_2_PROMPTS_EN_README_URL, {
      signal,
      headers: {
        Accept: "text/markdown,text/plain",
      },
    }),
  ]);
  if (!zhResponse.ok) {
    throw new Error(`读取 awesome-gpt-image-2-prompts 中文提示词失败：${zhResponse.status}`);
  }
  if (!enResponse.ok) {
    throw new Error(`读取 awesome-gpt-image-2-prompts 英文提示词失败：${enResponse.status}`);
  }

  const [zhMarkdown, enMarkdown] = await Promise.all([zhResponse.text(), enResponse.text()]);
  return mergeAwesomePrompts(
    parseAwesomePrompts(zhMarkdown, "zh-CN"),
    parseAwesomePrompts(enMarkdown, "en"),
  );
}

// --- Gallery Parser for freestylefly/awesome-gpt-image-2 format ---
const GALLERY_CASE_HEADING_PATTERN = /^###\s+(?:Case|例)\s*(\d+)[:：]\s*(.+)$/;
const GALLERY_ANCHOR_PATTERN = /^<a\s+name="case-(\d+)"><\/a>$/i;
const GALLERY_IMAGE_PATTERN = /(?:<img\s+[^>]*src=["']([^"']+)["'][^>]*>|!\[[^\]]*\]\(([^)]+)\))/i;
const GALLERY_PROMPT_PATTERN =
  /\*{2,}\s*(?:Prompt|提示词)\s*[:：]\s*\*{2,}\s*\r?\n\s*```(?:\w+)?\r?\n([\s\S]*?)\r?\n```/i;
const GALLERY_CATEGORY_PATTERN = /^## (.+)$/;
const IGNORED_GALLERY_HEADINGS = new Set([
  "简介",
  "最新动态",
  "Menu",
  "致谢",
  "Star History",
  "Featured Cases",
  "Latest Community Additions",
  "Canghe Original Tests",
]);

function normalizeGalleryImageUrl(value: string, baseUrl: string) {
  const imageUrl = value.trim();
  if (!imageUrl) {
    return "";
  }
  if (/^https?:\/\//i.test(imageUrl)) {
    return imageUrl;
  }
  return new URL(imageUrl.replace(/^\.\//, "").replace(/^\.\.\//, ""), baseUrl).toString();
}

function parseGalleryPrompts(
  markdown: string,
  language: PromptMarketLanguage,
  source: PromptMarketSourceId,
  baseUrl: string,
): BananaPrompt[] {
  const lines = markdown.split(/\r?\n/);
  const prompts: BananaPrompt[] = [];
  let activeCategory = "未分类";

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];

    // Track category from ## headings
    if (line.startsWith("## ")) {
      const headingMatch = line.match(GALLERY_CATEGORY_PATTERN);
      if (headingMatch) {
        const heading = cleanMarkdownHeading(headingMatch[1]);
        if (heading && !IGNORED_GALLERY_HEADINGS.has(heading)) {
          activeCategory = heading;
        }
      }
      continue;
    }

    // Look for ### Case N: Title pattern
    const caseMatch = line.match(GALLERY_CASE_HEADING_PATTERN);
    if (!caseMatch) {
      continue;
    }

    const caseNumber = caseMatch[1].trim();
    const title = caseMatch[2].trim();

    // Find the section end (next ### Case or ## heading)
    let sectionEnd = lines.length;
    for (let nextIndex = index + 1; nextIndex < lines.length; nextIndex += 1) {
      if (
        lines[nextIndex].match(GALLERY_CASE_HEADING_PATTERN) ||
        lines[nextIndex].startsWith("## ")
      ) {
        sectionEnd = nextIndex;
        break;
      }
    }

    const section = lines.slice(index, sectionEnd).join("\n");
    const imageMatch = section.match(GALLERY_IMAGE_PATTERN);
    const promptMatch = section.match(GALLERY_PROMPT_PATTERN);

    if (!imageMatch || !promptMatch) {
      index = sectionEnd - 1;
      continue;
    }

    const preview = normalizeGalleryImageUrl(imageMatch[1] || imageMatch[2] || "", baseUrl);
    const prompt = promptMatch[1].trim();

    if (!title || !preview || !prompt) {
      index = sectionEnd - 1;
      continue;
    }

    const id = `${source}:case-${caseNumber}`;
    prompts.push({
      id,
      title,
      preview,
      referenceImageUrls: [],
      prompt,
      author: "Community",
      link: source === "awesome-gpt-image-2-ch" ? AWESOME_GPT_IMAGE_2_CH_SOURCE_URL : AWESOME_GPT_IMAGE_2_SOURCE_URL,
      mode: "generate",
      category: activeCategory,
      subCategory: `Case ${caseNumber}`,
      source,
      sourceLabel: source === "awesome-gpt-image-2-ch" ? "awesome-gpt-image-2 (中文)" : "awesome-gpt-image-2 (EN)",
      isNsfw: isNsfwPrompt(activeCategory, title, prompt),
      localizations: {
        [language]: {
          title,
          prompt,
          category: activeCategory,
          subCategory: `Case ${caseNumber}`,
        },
      },
    });

    index = sectionEnd - 1;
  }

  return prompts;
}

export async function fetchAwesomeGptImage2GalleryPrompts(signal?: AbortSignal) {
  const [part1Response, part2Response] = await Promise.all([
    fetch(AWESOME_GPT_IMAGE_2_GALLERY_PART1_URL, {
      signal,
      headers: { Accept: "text/markdown,text/plain" },
    }),
    fetch(AWESOME_GPT_IMAGE_2_GALLERY_PART2_URL, {
      signal,
      headers: { Accept: "text/markdown,text/plain" },
    }),
  ]);

  const prompts: BananaPrompt[] = [];
  if (part1Response.ok) {
    const markdown = await part1Response.text();
    prompts.push(...parseGalleryPrompts(markdown, "en", "awesome-gpt-image-2", AWESOME_GPT_IMAGE_2_RAW_BASE_URL));
  }
  if (part2Response.ok) {
    const markdown = await part2Response.text();
    prompts.push(...parseGalleryPrompts(markdown, "en", "awesome-gpt-image-2", AWESOME_GPT_IMAGE_2_RAW_BASE_URL));
  }

  if (prompts.length === 0 && !part1Response.ok && !part2Response.ok) {
    throw new Error(`读取 awesome-gpt-image-2 gallery 失败：${part1Response.status}/${part2Response.status}`);
  }

  return prompts;
}

export async function fetchAwesomeGptImage2ChGalleryPrompts(signal?: AbortSignal) {
  const [part1Response, part2Response] = await Promise.all([
    fetch(AWESOME_GPT_IMAGE_2_CH_GALLERY_PART1_URL, {
      signal,
      headers: { Accept: "text/markdown,text/plain" },
    }),
    fetch(AWESOME_GPT_IMAGE_2_CH_GALLERY_PART2_URL, {
      signal,
      headers: { Accept: "text/markdown,text/plain" },
    }),
  ]);

  const prompts: BananaPrompt[] = [];
  if (part1Response.ok) {
    const markdown = await part1Response.text();
    prompts.push(...parseGalleryPrompts(markdown, "zh-CN", "awesome-gpt-image-2-ch", AWESOME_GPT_IMAGE_2_CH_RAW_BASE_URL));
  }
  if (part2Response.ok) {
    const markdown = await part2Response.text();
    prompts.push(...parseGalleryPrompts(markdown, "zh-CN", "awesome-gpt-image-2-ch", AWESOME_GPT_IMAGE_2_CH_RAW_BASE_URL));
  }

  if (prompts.length === 0 && !part1Response.ok && !part2Response.ok) {
    throw new Error(`读取 awesome-gpt-image-2-ch gallery 失败：${part1Response.status}/${part2Response.status}`);
  }

  return prompts;
}

export async function fetchPromptMarketPrompts(signal?: AbortSignal) {
  const [bananaPrompts, awesomePrompts, galleryEnPrompts, galleryChPrompts] = await Promise.all([
    fetchBananaPrompts(signal),
    fetchAwesomeGptImage2Prompts(signal),
    fetchAwesomeGptImage2GalleryPrompts(signal).catch((error) => {
      console.warn("Failed to fetch awesome-gpt-image-2 gallery:", error);
      return [] as BananaPrompt[];
    }),
    fetchAwesomeGptImage2ChGalleryPrompts(signal).catch((error) => {
      console.warn("Failed to fetch awesome-gpt-image-2-ch gallery:", error);
      return [] as BananaPrompt[];
    }),
  ]);

  return [...bananaPrompts, ...awesomePrompts, ...galleryEnPrompts, ...galleryChPrompts];
}
