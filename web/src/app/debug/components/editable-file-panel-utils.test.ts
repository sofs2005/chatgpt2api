import { describe, expect, it } from "vitest";

import { buildEditableFileDownloadHref, collectEditableFilePollIds } from "./editable-file-panel-utils";

describe("editable file panel utils", () => {
  it("builds encoded download hrefs", () => {
    expect(buildEditableFileDownloadHref("2026/06/01/deck.pptx")).toBe("/files/2026%2F06%2F01%2Fdeck.pptx");
  });

  it("returns an empty string for blank download paths", () => {
    expect(buildEditableFileDownloadHref("   ")).toBe("");
  });

  it("collects unique queued and running task ids", () => {
    expect(
      collectEditableFilePollIds([
        { taskId: "task-1", status: "queued" },
        { taskId: "task-1", status: "running" },
        { taskId: "task-2", status: "success" },
        { taskId: "task-3", status: "error" },
        { taskId: "task-4", status: "running" },
      ]),
    ).toEqual(["task-1", "task-4"]);
  });
});
