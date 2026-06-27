import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Toggle } from "./Toggle";

describe("Toggle", () => {
  it("reflects the checked state", () => {
    render(<Toggle checked onChange={() => {}} aria-label="clash" />);
    expect(screen.getByRole("switch", { name: "clash" })).toBeChecked();
  });

  it("emits the inverted value on click", async () => {
    const onChange = vi.fn();
    render(<Toggle checked={false} onChange={onChange} aria-label="clash" />);
    await userEvent.click(screen.getByRole("switch", { name: "clash" }));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("does not emit when disabled", async () => {
    const onChange = vi.fn();
    render(<Toggle checked={false} disabled onChange={onChange} aria-label="clash" />);
    await userEvent.click(screen.getByRole("switch", { name: "clash" }));
    expect(onChange).not.toHaveBeenCalled();
  });
});
