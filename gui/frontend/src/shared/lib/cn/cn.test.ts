import { describe, expect, it } from "vitest";

import { cn } from "./cn";

describe("cn", () => {
  it("joins truthy class names", () => {
    expect(cn("a", "b")).toBe("a b");
  });

  it("drops falsy values and supports object form", () => {
    expect(cn("a", false, null, undefined, { b: true, c: false })).toBe("a b");
  });

  it("merges conflicting tailwind utilities, last wins", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
  });
});
