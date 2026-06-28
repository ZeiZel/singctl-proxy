import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Textarea } from "./Textarea";

describe("Textarea", () => {
  it("forwards typed input", async () => {
    const onChange = vi.fn();
    render(<Textarea aria-label="token" onChange={onChange} />);
    await userEvent.type(screen.getByLabelText("token"), "ab");
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("respects the disabled attribute", () => {
    render(<Textarea aria-label="token" disabled />);
    expect(screen.getByLabelText("token")).toBeDisabled();
  });
});
