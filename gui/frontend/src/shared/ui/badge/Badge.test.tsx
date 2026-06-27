import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Badge } from "./Badge";

describe("Badge", () => {
  it("renders its children", () => {
    render(<Badge>42</Badge>);
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("applies the tone variant", () => {
    render(<Badge tone="danger">err</Badge>);
    expect(screen.getByText("err")).toHaveClass("text-danger");
  });
});
