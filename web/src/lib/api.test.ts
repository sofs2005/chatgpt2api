import { describe, expect, it } from "vitest";

import { buildEditableFileDownloadUrl } from "@/lib/api";

describe("buildEditableFileDownloadUrl", () => {
  it("encodes relative file paths for download urls", () => {
    expect(buildEditableFileDownloadUrl("2026/06/01/deck.pptx")).toBe("/files/2026%2F06%2F01%2Fdeck.pptx");
  });

  it("returns an empty string for blank input", () => {
    expect(buildEditableFileDownloadUrl("   ")).toBe("");
  });
});
