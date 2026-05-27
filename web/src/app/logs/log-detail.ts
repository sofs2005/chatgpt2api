import type { SystemLog } from "@/lib/api";

export const detailLabels: Record<string, string> = {
  endpoint: "接口",
  model: "模型",
  method: "方法",
  path: "路径",
  module: "模块",
  status: "状态",
  outcome: "结果",
  log_level: "日志级别",
  operation_type: "操作类型",
  duration_ms: "耗时",
  response_time: "响应时间",
  started_at: "开始时间",
  ended_at: "结束时间",
  username: "操作人",
  key_name: "令牌名称",
  session_name: "会话名称",
  auth_kind: "认证方式",
  key_role: "角色",
  key_id: "凭据 ID",
  subject_id: "用户 ID",
  provider: "来源",
  ip_address: "IP 地址",
  user_agent: "User-Agent",
  error: "错误",
  token: "令牌",
  source: "来源事件",
  added: "新增",
  skipped: "跳过",
  removed: "删除",
  upstream_account_name: "上游账号",
  upstream_account_names: "上游账号列表",
};

export const summaryDetailKeys = new Set([
  "method",
  "path",
  "endpoint",
  "module",
  "status",
  "outcome",
  "log_level",
  "duration_ms",
  "response_time",
  "upstream_account_name",
  "upstream_account_names",
]);

export const detailSectionDefinitions = [
  {
    title: "请求",
    keys: ["operation_type", "ip_address", "user_agent", "model"],
  },
  {
    title: "身份",
    keys: ["username", "key_name", "session_name", "auth_kind", "key_role", "subject_id", "key_id", "provider"],
  },
  {
    title: "时间",
    keys: ["started_at", "ended_at"],
  },
] as const;

function normalizeUpstreamAccountName(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

export function getUpstreamAccountText(item: SystemLog | null) {
  const detail = item?.detail;
  if (!detail) {
    return "—";
  }

  const upstreamAccountName = normalizeUpstreamAccountName(detail.upstream_account_name);
  if (upstreamAccountName) {
    return upstreamAccountName;
  }

  const upstreamAccountNames = detail.upstream_account_names;
  if (Array.isArray(upstreamAccountNames)) {
    const names = upstreamAccountNames.map(normalizeUpstreamAccountName).filter((name): name is string => name.length > 0);
    if (names.length > 0) {
      return names.join("、");
    }
  }

  return "—";
}
