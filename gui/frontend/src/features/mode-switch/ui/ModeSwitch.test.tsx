import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/shared/api/singctl";

import { ModeSwitch } from "./ModeSwitch";

vi.mock("@/shared/api/singctl", () => ({
  api: { setMode: vi.fn() },
}));

const setMode = vi.mocked(api.setMode);

beforeEach(() => {
  setMode.mockReset();
});

describe("ModeSwitch", () => {
  it("calls api.setMode with the chosen mode", async () => {
    setMode.mockResolvedValue();
    render(<ModeSwitch mode="off" />);
    await userEvent.click(screen.getByRole("button", { name: "VPN" }));
    expect(setMode).toHaveBeenCalledWith("vpn");
  });

  it("surfaces an error when the switch fails", async () => {
    setMode.mockRejectedValue(new Error("no daemon"));
    render(<ModeSwitch mode="off" />);
    await userEvent.click(screen.getByRole("button", { name: "Proxy" }));
    await waitFor(() => expect(screen.getByText(/no daemon/)).toBeInTheDocument());
  });
});
