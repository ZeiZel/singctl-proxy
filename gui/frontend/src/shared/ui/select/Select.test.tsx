import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Select } from "./Select";

describe("Select", () => {
  it("emits the chosen option value", async () => {
    const onChange = vi.fn();
    render(
      <Select aria-label="app" onChange={onChange}>
        <option value="">all</option>
        <option value="zen">zen</option>
      </Select>
    );
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "app" }), "zen");
    expect(onChange).toHaveBeenCalledOnce();
  });
});
