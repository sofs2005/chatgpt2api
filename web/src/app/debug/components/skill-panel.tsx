import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

const references = [
  { title: "账号路径", body: "所有探针必须复用 Engine 与 AccountService，不直接创建新的上游客户端。" },
  { title: "安全边界", body: "只发送单条查询，不上传文件、不暴露令牌、不启用流式输出。" },
  { title: "结果留存", body: "排障时优先保存 payload、result 和 /api/logs 中的相邻记录。" },
];

export function SkillsReferencePanel() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Skills Reference</CardTitle>
        <CardDescription>调试台操作约束和迁移任务上下文速查。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 md:grid-cols-3">
        {references.map((item) => (
          <article key={item.title} className="rounded-xl border border-border bg-muted/35 p-4">
            <h3 className="mb-2 text-sm font-semibold text-foreground">{item.title}</h3>
            <p className="text-sm leading-6 text-muted-foreground">{item.body}</p>
          </article>
        ))}
      </CardContent>
    </Card>
  );
}
