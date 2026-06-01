import { buildEditableFileDownloadUrl } from "@/lib/api";
import type { EditableFileHistoryItem } from "@/store/editable-file-history";

export const buildEditableFileDownloadHref = buildEditableFileDownloadUrl;

export function collectEditableFilePollIds(items: Array<Pick<EditableFileHistoryItem, "status" | "taskId">>) {
  const ids = new Set<string>();

  for (const item of items) {
    if (item.status !== "queued" && item.status !== "running") {
      continue;
    }
    const taskId = item.taskId.trim();
    if (taskId) {
      ids.add(taskId);
    }
  }

  return Array.from(ids).sort();
}
