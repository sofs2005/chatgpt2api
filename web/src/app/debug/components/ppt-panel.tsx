"use client";

import { useState, type FormEvent } from "react";
import { LoaderCircle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { createPptGenerationTask, type EditableFileTask } from "@/lib/api";

export type EditableFileTaskCreatedHandler = (task: EditableFileTask, prompt: string, clientTaskId: string) => void | Promise<void>;

type PptPanelProps = {
  onTaskCreated?: EditableFileTaskCreatedHandler;
};

export function PptPanel({ onTaskCreated }: PptPanelProps) {
  const [prompt, setPrompt] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const nextPrompt = prompt.trim();
    if (!nextPrompt) {
      setError("请输入 PPT 提示词");
      return;
    }

    setSubmitting(true);
    setError("");

    try {
      const clientTaskId = `editable-file-ppt-${crypto.randomUUID()}`;
      const task = await createPptGenerationTask({ prompt: nextPrompt, clientTaskId });
      await onTaskCreated?.(task, nextPrompt, clientTaskId);
    } catch (err) {
      setError(err instanceof Error ? err.message : "PPT 任务创建失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className="space-y-3" onSubmit={handleSubmit}>
      <label className="grid gap-2 text-sm font-medium text-foreground">
        提示词
        <Textarea
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          placeholder="输入 PPT 生成提示词"
          className="min-h-[120px] rounded-2xl border-border bg-background text-sm leading-6 shadow-none"
        />
      </label>
      {error ? <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div> : null}
      <Button type="submit" disabled={submitting} className="rounded-xl">
        {submitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
        生成 PPT
      </Button>
    </form>
  );
}
