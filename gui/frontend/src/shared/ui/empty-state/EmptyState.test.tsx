import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { EmptyState } from "./EmptyState";

describe("EmptyState", () => {
  it("renders its message with a status role", () => {
    render(<EmptyState>No data</EmptyState>);
    expect(screen.getByRole("status")).toHaveTextContent("No data");
  });
});
