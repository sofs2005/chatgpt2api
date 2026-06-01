"use client";

import { useMemo, useState, type FormEvent } from "react";
import { LoaderCircle, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { runDebugSearchProbe, type DebugSearchResponse } from "@/lib/api";

function stringifyJSON(value: unknown) {
  return JSON.stringify(value, null, 2);
}

function extractResultText(result: DebugSearchResponse | null) {
  const choices = result?.result?.choices;
  if (!Array.isArray(choices)) return "";
  const first = choices[0];
  if (!first || typeof first !== "object" || !("message" in first)) return "";
  const message = first.message;
  if (!message || typeof message !== "object" || !("content" in message)) return "";
  const content = message.content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .map((part) => (part && typeof part === "object" && "text" in part && typeof part.text === "string" ? part.text : ""))
    .filter(Boolean)
    .join("\n");
}

export function SearchProbePanel() {
  const [query, setQuery] = useState("");
  const [model, setModel] = useState("auto");
  const [result, setResult] = useState<DebugSearchResponse | null>(null);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const rawResult = useMemo(() => (result ? JSON.stringify(result) : ""), [result]);
  const prettyResult = useMemo(() => stringifyJSON(result), [result]);
  const resultText = useMemo(() => extractResultText(result), [result]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextQuery = query.trim();
    if (!nextQuery) {
      setError("请输入要探测的查询内容");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      const data = await runDebugSearchProbe({ query: nextQuery, model: model.trim() || "auto" });
      setResult(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Search 探针请求失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Search Probe</CardTitle>
        <CardDescription>提交一个查询，通过后端当前账号调度路径执行非流式 Chat Completions 探针。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <form className="grid gap-3 lg:grid-cols-[1fr_180px_auto]" onSubmit={handleSubmit}>
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入搜索/调试查询" />
          <Input value={model} onChange={(event) => setModel(event.target.value)} placeholder="模型，例如 auto" />
          <Button type="submit" disabled={submitting}>
            {submitting ? <LoaderCircle className="size-4 animate-spin" /> : <Search className="size-4" />}
            执行探针
          </Button>
        </form>
        {error ? <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div> : null}
        {resultText ? (
          <div className="rounded-xl border border-border bg-muted/40 p-4">
            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">Prettified Result</div>
            <div className="whitespace-pre-wrap text-sm leading-6 text-foreground">{resultText}</div>
          </div>
        ) : null}
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-2">
            <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Raw Response</div>
            <Textarea className="min-h-[260px] font-mono text-xs" readOnly value={rawResult} placeholder="后端原始响应 JSON 将显示在这里" />
          </div>
          <div className="space-y-2">
            <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Pretty JSON</div>
            <Textarea className="min-h-[260px] font-mono text-xs" readOnly value={prettyResult} placeholder="格式化 JSON 将显示在这里" />
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
