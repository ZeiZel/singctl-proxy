import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api, type TSettings } from "@/shared/api/singctl";

import { SettingsForm } from "./SettingsForm";

vi.mock("@/shared/api/singctl", () => ({
  api: { getSettings: vi.fn(), applySettings: vi.fn() },
}));

const mocked = vi.mocked(api);

const SETTINGS: TSettings = {
  SocksPort: 1080,
  ClashEnabled: true,
  ClashAddr: "127.0.0.1:9090",
  URLTestURL: "https://gstatic.com/generate_204",
  URLTestInterval: "3m",
  URLTestTolerance: 50,
  SaveProfile: true,
};

beforeEach(() => {
  mocked.getSettings.mockReset();
  mocked.applySettings.mockReset();
});

describe("SettingsForm", () => {
  it("loads and shows the current settings", async () => {
    mocked.getSettings.mockResolvedValue(SETTINGS);
    render(<SettingsForm onStopDaemon={() => {}} />);
    expect(await screen.findByLabelText("socks-port")).toHaveValue(1080);
  });

  it("applies edited settings", async () => {
    mocked.getSettings.mockResolvedValue(SETTINGS);
    mocked.applySettings.mockResolvedValue();
    render(<SettingsForm onStopDaemon={() => {}} />);
    await screen.findByLabelText("socks-port");
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(mocked.applySettings).toHaveBeenCalledWith(SETTINGS);
  });

  it("invokes onStopDaemon", async () => {
    const onStopDaemon = vi.fn();
    mocked.getSettings.mockResolvedValue(SETTINGS);
    render(<SettingsForm onStopDaemon={onStopDaemon} />);
    await screen.findByLabelText("socks-port");
    await userEvent.click(screen.getByRole("button", { name: "Stop daemon" }));
    expect(onStopDaemon).toHaveBeenCalledOnce();
  });
});
