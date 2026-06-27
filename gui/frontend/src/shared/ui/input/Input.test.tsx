import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Input } from "./Input";

describe("Input", () => {
  it("forwards typed value to onChange", async () => {
    const onChange = vi.fn();
    render(<Input placeholder="key" onChange={onChange} />);
    await userEvent.type(screen.getByPlaceholderText("key"), "ab");
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("respects the disabled attribute", () => {
    render(<Input placeholder="key" disabled />);
    expect(screen.getByPlaceholderText("key")).toBeDisabled();
  });
});
