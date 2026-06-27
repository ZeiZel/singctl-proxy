import { describe, expect, it } from "vitest";

import { formatBytes, formatRate } from "./format";

describe("formatBytes", () => {
  it("renders bytes below 1 KB verbatim", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  it("scales into KB/MB/GB", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(5 * 1024 * 1024)).toBe("5.0 MB");
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe("3.0 GB");
  });

  it("drops the decimal for values >= 100", () => {
    expect(formatBytes(200 * 1024)).toBe("200 KB");
  });
});

describe("formatRate", () => {
  it("appends a per-second suffix", () => {
    expect(formatRate(0)).toBe("0 B/s");
    expect(formatRate(2048)).toBe("2.0 KB/s");
  });
});
