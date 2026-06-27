import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { IconDashboard } from "./Icon";

describe("IconDashboard", () => {
  it("renders an svg and forwards props", () => {
    const { container } = render(<IconDashboard className="icon" aria-label="dashboard" />);
    const svg = container.querySelector("svg");
    expect(svg).toBeInTheDocument();
    expect(svg).toHaveClass("icon");
    expect(svg).toHaveAttribute("aria-label", "dashboard");
  });
});
