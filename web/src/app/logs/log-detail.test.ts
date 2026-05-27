import { describe, expect, it } from "vitest";

import type { SystemLog } from "@/lib/api";

import { getUpstreamAccountText } from "./log-detail";

function makeLog(detail: SystemLog["detail"]): SystemLog {
  return {
    time: "2026-01-01 00:00:00",
    detail,
  };
}

describe("getUpstreamAccountText", () => {
  it("returns a single upstream_account_name", () => {
    expect(getUpstreamAccountText(makeLog({ upstream_account_name: "Alpha" }))).toBe("Alpha");
  });

  it("returns multiple upstream_account_names joined with 、", () => {
    expect(getUpstreamAccountText(makeLog({ upstream_account_names: ["Alpha", "Beta"] }))).toBe("Alpha、Beta");
  });

  it("prefers the single upstream_account_name when both fields exist", () => {
    expect(getUpstreamAccountText(makeLog({ upstream_account_name: "Alpha", upstream_account_names: ["Beta"] }))).toBe("Alpha");
  });

  it("returns an em dash when upstream account data is missing", () => {
    expect(getUpstreamAccountText(makeLog(undefined))).toBe("—");
    expect(getUpstreamAccountText(null)).toBe("—");
  });
});
