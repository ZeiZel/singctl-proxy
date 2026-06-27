import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Box } from "./Box";

describe("Box", () => {
  it("renders children", () => {
    render(<Box>content</Box>);
    expect(screen.getByText("content")).toBeInTheDocument();
  });

  it("merges custom class names", () => {
    render(<Box className="text-danger">x</Box>);
    expect(screen.getByText("x")).toHaveClass("text-danger");
  });
});
