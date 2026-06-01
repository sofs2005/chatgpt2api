"use client";

import { useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { ImagePlus, LoaderCircle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { createPsdGenerationTask } from "@/lib/api";

import type { EditableFileTaskCreatedHandler } from "./ppt-panel";

type EditableFileImageDraft = {
  name: string;
  dataUrl: string;
};

type PsdPanelProps = {
  onTaskCreated?: EditableFileTaskCreatedHandler;
};

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => reject(reader.error ?? new Error(`读取图片失败: ${file.name}`));
    reader.readAsDataURL(file);
  });
}

export function PsdPanel({ onTaskCreated }: PsdPanelProps) {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [prompt, setPrompt] = useState("");
  const [images, setImages] = useState<EditableFileImageDraft[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const handleFileChange = async (event: ChangeEvent<HTMLInputElement>) => {
    const nextFiles = Array.from(event.target.files || []);
    if (nextFiles.length === 0) {
      setImages([]);
      return;
    }

    try {
      const nextImages = await Promise.all(
        nextFiles.map(async (file) => ({
          name: file.name,
          dataUrl: await readFileAsDataUrl(file),
        })),
      );
      setImages(nextImages);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "读取图片失败");
    } finally {
      event.target.value = "";
    }
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const nextPrompt = prompt.trim();
    if (!nextPrompt) {
      setError("请输入 PSD 提示词");
      return;
    }
    if (images.length === 0) {
      setError("请至少选择一张图片");
      return;
    }

    setSubmitting(true);
    setError("");

    try {
      const clientTaskId = `editable-file-psd-${crypto.randomUUID()}`;
      const base64Images = images.map((image) => image.dataUrl.split(",", 2)[1] || "");
      const task = await createPsdGenerationTask({ prompt: nextPrompt, base64Images, clientTaskId });
      await onTaskCreated?.(task, nextPrompt, clientTaskId);
    } catch (err) {
      setError(err instanceof Error ? err.message : "PSD 任务创建失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className="space-y-4" onSubmit={handleSubmit}>
      <label className="grid gap-2 text-sm font-medium text-foreground">
        提示词
        <Textarea
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          placeholder="输入 PSD 生成提示词"
          className="min-h-[120px] rounded-2xl border-border bg-background text-sm leading-6 shadow-none"
        />
      </label>

      <div className="space-y-3">
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          multiple
          className="hidden"
          onChange={handleFileChange}
        />
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            variant="outline"
            className="rounded-xl"
            onClick={() => fileInputRef.current?.click()}
          >
            <ImagePlus className="size-4" />
            选择图片
          </Button>
          <div className="text-sm text-muted-foreground">
            已选择 {images.length} 张图片
          </div>
        </div>

        {images.length > 0 ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {images.map((image, index) => (
              <figure key={`${image.name}-${index}`} className="overflow-hidden rounded-2xl border border-border bg-muted/35">
                <div className="aspect-[4/3] bg-muted">
                  <img src={image.dataUrl} alt={image.name} className="h-full w-full object-cover" />
                </div>
                <figcaption className="border-t border-border px-3 py-2 text-xs text-muted-foreground">
                  {image.name}
                </figcaption>
              </figure>
            ))}
          </div>
        ) : null}
      </div>

      {error ? <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div> : null}
      <Button type="submit" disabled={submitting || images.length === 0} className="rounded-xl">
        {submitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
        生成 PSD
      </Button>
    </form>
  );
}
