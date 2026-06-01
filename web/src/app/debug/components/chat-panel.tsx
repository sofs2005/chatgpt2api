import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

const traceSteps = [
  "确认前端请求体、模型和 stream=false 设置。",
  "检查 /api/logs 中同一时间窗口的文本生成记录。",
  "核对账号状态、Cookie 完整性和上游限流恢复时间。",
  "必要时用 Search Probe 复现最小查询并保存原始 JSON。",
];

export function ChatTracePanel() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Chat Trace</CardTitle>
        <CardDescription>用于值班排障的静态流程卡片，后续可接入实时请求链路。</CardDescription>
      </CardHeader>
      <CardContent>
        <ol className="grid gap-3 md:grid-cols-2">
          {traceSteps.map((step, index) => (
            <li key={step} className="rounded-xl border border-border bg-muted/35 p-4 text-sm leading-6">
              <div className="mb-2 text-xs font-semibold text-muted-foreground">Step {index + 1}</div>
              {step}
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}
