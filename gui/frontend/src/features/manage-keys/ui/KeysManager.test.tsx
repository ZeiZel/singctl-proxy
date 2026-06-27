import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/shared/api/singctl";

import { KeysManager } from "./KeysManager";

vi.mock("@/shared/api/singctl", () => ({
  api: { getKeys: vi.fn(), addKey: vi.fn(), renameKey: vi.fn(), deleteKey: vi.fn() },
}));

const mocked = vi.mocked(api);

beforeEach(() => {
  mocked.getKeys.mockReset();
  mocked.addKey.mockReset();
});

describe("KeysManager", () => {
  it("renders loaded keys (masked)", async () => {
    mocked.getKeys.mockResolvedValue([{ index: 0, name: "Tokyo", masked: "vless://••••@h:443" }]);
    render(<KeysManager />);
    expect(await screen.findByText("Tokyo")).toBeInTheDocument();
    expect(screen.getByText("vless://••••@h:443")).toBeInTheDocument();
  });

  it("adds a key via the input", async () => {
    mocked.getKeys.mockResolvedValue([]);
    mocked.addKey.mockResolvedValue();
    render(<KeysManager />);
    await userEvent.type(screen.getByLabelText("new-key"), "vless://x@h:1");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(mocked.addKey).toHaveBeenCalledWith("vless://x@h:1");
  });

  it("shows an empty state when there are no keys", async () => {
    mocked.getKeys.mockResolvedValue([]);
    render(<KeysManager />);
    expect(await screen.findByText("No keys loaded.")).toBeInTheDocument();
  });
});
