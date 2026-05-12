export const SIMILAR_IMAGE_INTENT_STORAGE_KEY = "chatgpt2api:image_similar_intent";

export type SimilarImageIntent = {
  id: string;
  createdAt: string;
  prompt: string;
  sourceImageUrl: string;
  sourceImageUrls: string[];
  sourceKind?: "original_references" | "public_image";
  sourceImageName?: string;
  model?: string;
  quality?: string;
  requestedSize?: string;
  resolutionPreset?: string;
  outputFormat?: string;
  outputCompression?: number;
};

type SimilarImageIntentInput = Omit<SimilarImageIntent, "id" | "createdAt" | "sourceImageUrl" | "sourceImageUrls"> & {
  sourceImageUrl?: string;
  sourceImageUrls?: string[];
};

function normalizeSourceImageUrls(sourceImageUrls?: string[], sourceImageUrl?: string) {
  return Array.from(
    new Set(
      [...(sourceImageUrls || []), sourceImageUrl || ""]
        .map((url) => url.trim())
        .filter(Boolean),
    ),
  );
}

export function writeSimilarImageIntent(intent: SimilarImageIntentInput) {
  const sourceImageUrls = normalizeSourceImageUrls(intent.sourceImageUrls, intent.sourceImageUrl);
  if (sourceImageUrls.length === 0) {
    return;
  }
  window.localStorage.setItem(
    SIMILAR_IMAGE_INTENT_STORAGE_KEY,
    JSON.stringify({
      ...intent,
      sourceImageUrl: sourceImageUrls[0],
      sourceImageUrls,
      sourceKind: intent.sourceKind === "original_references" ? "original_references" : "public_image",
      id: typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(16).slice(2)}`,
      createdAt: new Date().toISOString(),
    }),
  );
}

export function consumeSimilarImageIntent(): SimilarImageIntent | null {
  const raw = window.localStorage.getItem(SIMILAR_IMAGE_INTENT_STORAGE_KEY);
  if (!raw) {
    return null;
  }
  window.localStorage.removeItem(SIMILAR_IMAGE_INTENT_STORAGE_KEY);

  try {
    const parsed = JSON.parse(raw) as Partial<SimilarImageIntent>;
    const sourceImageUrls = normalizeSourceImageUrls(
      Array.isArray(parsed.sourceImageUrls) ? parsed.sourceImageUrls.filter((url): url is string => typeof url === "string") : [],
      typeof parsed.sourceImageUrl === "string" ? parsed.sourceImageUrl : "",
    );
    if (sourceImageUrls.length === 0) {
      return null;
    }
    return {
      id: typeof parsed.id === "string" ? parsed.id : "",
      createdAt: typeof parsed.createdAt === "string" ? parsed.createdAt : "",
      prompt: typeof parsed.prompt === "string" ? parsed.prompt : "",
      sourceImageUrl: sourceImageUrls[0],
      sourceImageUrls,
      sourceKind: parsed.sourceKind === "original_references" ? "original_references" : "public_image",
      sourceImageName: typeof parsed.sourceImageName === "string" ? parsed.sourceImageName : undefined,
      model: typeof parsed.model === "string" ? parsed.model : undefined,
      quality: typeof parsed.quality === "string" ? parsed.quality : undefined,
      requestedSize: typeof parsed.requestedSize === "string" ? parsed.requestedSize : undefined,
      resolutionPreset: typeof parsed.resolutionPreset === "string" ? parsed.resolutionPreset : undefined,
      outputFormat: typeof parsed.outputFormat === "string" ? parsed.outputFormat : undefined,
      outputCompression: typeof parsed.outputCompression === "number" ? parsed.outputCompression : undefined,
    };
  } catch {
    return null;
  }
}
