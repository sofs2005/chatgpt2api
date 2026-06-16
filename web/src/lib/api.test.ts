import { describe, expect, it } from "vitest";

import { buildEditableFileDownloadUrl, getImageProgressLabel, isResumableTimeoutImage } from "@/lib/api";

describe("buildEditableFileDownloadUrl", () => {
  it("encodes relative file paths for download urls", () => {
    expect(buildEditableFileDownloadUrl("2026/06/01/deck.pptx")).toBe("/files/2026%2F06%2F01%2Fdeck.pptx");
  });

  it("returns an empty string for blank input", () => {
    expect(buildEditableFileDownloadUrl("   ")).toBe("");
  });
});

describe("getImageProgressLabel", () => {
  it("maps known backend progress steps to friendly Chinese labels", () => {
    expect(getImageProgressLabel("getting_account")).toBe("确认可用账号");
    expect(getImageProgressLabel("receiving_image")).toBe("接收图片中");
    expect(getImageProgressLabel("image_stream_resolve_start")).toBe("生成中");
  });

  it("falls back to a generic label for empty or unknown steps", () => {
    expect(getImageProgressLabel(undefined)).toBe("生成中");
    expect(getImageProgressLabel("   ")).toBe("生成中");
    expect(getImageProgressLabel("something_unexpected")).toBe("生成中");
  });
});

describe("isResumableTimeoutImage", () => {
  it("returns true only for an errored timeout image that still has a task id", () => {
    expect(
      isResumableTimeoutImage({ status: "error", error: "图片生成超时，请稍后重试或降低分辨率", taskId: "task-1" }),
    ).toBe(true);
  });

  it("returns false when the task id is missing", () => {
    expect(isResumableTimeoutImage({ status: "error", error: "生图超时", taskId: undefined })).toBe(false);
  });

  it("returns false when the error is not a timeout", () => {
    expect(isResumableTimeoutImage({ status: "error", error: "用户余额不足", taskId: "task-1" })).toBe(false);
  });

  it("returns false for non-error statuses or empty errors", () => {
    expect(isResumableTimeoutImage({ status: "success", error: "超时", taskId: "task-1" })).toBe(false);
    expect(isResumableTimeoutImage({ status: "error", error: undefined, taskId: "task-1" })).toBe(false);
  });
});
