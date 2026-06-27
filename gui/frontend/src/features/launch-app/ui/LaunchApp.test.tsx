import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/shared/api/singctl";

import { LaunchApp } from "./LaunchApp";

vi.mock("@/shared/api/singctl", () => ({ api: { launchApp: vi.fn() } }));

const launchApp = vi.mocked(api.launchApp);

beforeEach(() => launchApp.mockReset());

describe("LaunchApp", () => {
  it("splits the command into argv and launches", async () => {
    launchApp.mockResolvedValue(1234);
    render(<LaunchApp />);
    await userEvent.type(screen.getByLabelText("launch-command"), "curl https://example.com");
    await userEvent.click(screen.getByRole("button", { name: "Launch" }));
    expect(launchApp).toHaveBeenCalledWith(["curl", "https://example.com"]);
  });

  it("disables the button while the command is empty", () => {
    render(<LaunchApp />);
    expect(screen.getByRole("button", { name: "Launch" })).toBeDisabled();
  });
});
