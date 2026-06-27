import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Stack } from "./Stack";

describe("Stack", () => {
  it("applies default column direction", () => {
    render(<Stack data-testid="s">x</Stack>);
    expect(screen.getByTestId("s")).toHaveClass("flex", "flex-col");
  });

  it("applies row direction and gap variants", () => {
    render(
      <Stack data-testid="s" direction="row" gap="lg" justify="between">
        x
      </Stack>
    );
    const node = screen.getByTestId("s");
    expect(node).toHaveClass("flex-row", "gap-6", "justify-between");
  });
});
