import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api, type TLicenseInfo } from "@/shared/api/singctl";

import { LicenseManager } from "./LicenseManager";

vi.mock("@/shared/api/singctl", () => ({
  api: { getLicense: vi.fn(), activateLicense: vi.fn(), removeLicense: vi.fn() },
}));

const mocked = vi.mocked(api);

const invalid: TLicenseInfo = {
  enforced: true,
  valid: false,
  subject: "",
  expiresAt: 0,
  daysLeft: -1,
  features: [],
  reason: "no license installed",
};

beforeEach(() => {
  mocked.getLicense.mockReset();
  mocked.activateLicense.mockReset();
  mocked.removeLicense.mockReset();
});

describe("LicenseManager", () => {
  it("shows the invalid reason and an activation form", async () => {
    mocked.getLicense.mockResolvedValue(invalid);
    render(<LicenseManager />);
    expect(await screen.findByText(/no license installed/)).toBeInTheDocument();
    expect(screen.getByLabelText("license-token")).toBeInTheDocument();
  });

  it("activates the pasted token with the entered email", async () => {
    mocked.getLicense.mockResolvedValue(invalid);
    mocked.activateLicense.mockResolvedValue();
    render(<LicenseManager />);
    await screen.findByLabelText("license-token");
    await userEvent.type(screen.getByLabelText("license-email"), "alice@example.com");
    await userEvent.type(screen.getByLabelText("license-token"), "SINGCTL-LIC.v1.abc");
    await userEvent.click(screen.getByRole("button", { name: "Activate" }));
    expect(mocked.activateLicense).toHaveBeenCalledWith("SINGCTL-LIC.v1.abc", "alice@example.com");
  });

  it("disables Activate until a valid email and a token are both present", async () => {
    mocked.getLicense.mockResolvedValue(invalid);
    render(<LicenseManager />);
    await screen.findByLabelText("license-token");
    const activateButton = screen.getByRole("button", { name: "Activate" });
    expect(activateButton).toBeDisabled();

    await userEvent.type(screen.getByLabelText("license-token"), "SINGCTL-LIC.v1.abc");
    expect(activateButton).toBeDisabled(); // email still missing

    await userEvent.type(screen.getByLabelText("license-email"), "not-an-email");
    expect(activateButton).toBeDisabled(); // email still invalid

    await userEvent.type(screen.getByLabelText("license-email"), "@example.com");
    expect(activateButton).toBeEnabled();
  });

  it("shows a clear error when activation reports the license was superseded", async () => {
    mocked.getLicense.mockResolvedValue(invalid);
    mocked.activateLicense.mockRejectedValue(
      new Error("Ключ активирован на другом устройстве. Активируйте его заново здесь.")
    );
    render(<LicenseManager />);
    await screen.findByLabelText("license-token");
    await userEvent.type(screen.getByLabelText("license-email"), "alice@example.com");
    await userEvent.type(screen.getByLabelText("license-token"), "SINGCTL-LIC.v1.abc");
    await userEvent.click(screen.getByRole("button", { name: "Activate" }));
    expect(await screen.findByText(/активирован на другом устройстве/)).toBeInTheDocument();
  });

  it("hides the form for a development build", async () => {
    mocked.getLicense.mockResolvedValue({ ...invalid, enforced: false, valid: true });
    render(<LicenseManager />);
    await waitFor(() => expect(screen.getByText(/Development build/)).toBeInTheDocument());
    expect(screen.queryByLabelText("license-token")).not.toBeInTheDocument();
  });
});
