import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Card } from "./Card";

describe("Card", () => {
  it("renders children inside a panel surface", () => {
    render(<Card>body</Card>);
    const node = screen.getByText("body");
    expect(node).toHaveAttribute("data-slot", "card");
    expect(node).toHaveClass("bg-panel/80", "backdrop-blur-md");
  });
});
