import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Button } from "./Button";

describe("Button", () => {
  it("fires onClick when enabled", async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>go</Button>);
    await userEvent.click(screen.getByRole("button", { name: "go" }));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("does not fire onClick when disabled", async () => {
    const onClick = vi.fn();
    render(
      <Button disabled onClick={onClick}>
        no
      </Button>
    );
    await userEvent.click(screen.getByRole("button", { name: "no" }));
    expect(onClick).not.toHaveBeenCalled();
  });

  it("applies the primary variant classes", () => {
    render(<Button variant="primary">p</Button>);
    expect(screen.getByRole("button", { name: "p" })).toHaveClass("bg-accent");
  });
});
