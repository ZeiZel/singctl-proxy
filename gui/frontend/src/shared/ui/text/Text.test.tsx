import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Heading } from "./Heading";
import { Text } from "./Text";

describe("Text", () => {
  it("renders with tone and mono variants", () => {
    render(
      <Text tone="danger" mono>
        boom
      </Text>
    );
    expect(screen.getByText("boom")).toHaveClass("text-danger", "font-mono");
  });
});

describe("Heading", () => {
  it("renders the element matching the level", () => {
    render(<Heading level={2}>Title</Heading>);
    const node = screen.getByRole("heading", { level: 2 });
    expect(node).toHaveTextContent("Title");
  });

  it("defaults to an h1", () => {
    render(<Heading>Main</Heading>);
    expect(screen.getByRole("heading", { level: 1 })).toBeInTheDocument();
  });
});
