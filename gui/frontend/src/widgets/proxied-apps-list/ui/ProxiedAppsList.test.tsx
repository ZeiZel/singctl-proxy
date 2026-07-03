import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { type TProxiedApp } from "@/shared/api/singctl";

import { ProxiedAppsList } from "./ProxiedAppsList";

const APPS: TProxiedApp[] = [
  { bundleID: "com.todesktop.cursor", name: "Cursor", enabled: true, running: true },
];

describe("ProxiedAppsList", () => {
  it("shows an empty state with nothing routed", () => {
    render(<ProxiedAppsList apps={[]} busy={false} onToggle={() => {}} onRemove={() => {}} />);
    expect(screen.getByText("No applications routed.")).toBeInTheDocument();
  });

  it("removes the chosen bundle id", async () => {
    const onRemove = vi.fn();
    render(<ProxiedAppsList apps={APPS} busy={false} onToggle={() => {}} onRemove={onRemove} />);
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Удалить" }));
    expect(onRemove).toHaveBeenCalledWith("com.todesktop.cursor");
  });

  it("toggles the enabled state", async () => {
    const onToggle = vi.fn();
    render(<ProxiedAppsList apps={APPS} busy={false} onToggle={onToggle} onRemove={() => {}} />);
    await userEvent.click(screen.getByRole("switch", { name: "toggle-com.todesktop.cursor" }));
    expect(onToggle).toHaveBeenCalledWith("com.todesktop.cursor", false);
  });

  it("falls back to a placeholder when the app name is unknown", () => {
    const apps: TProxiedApp[] = [{ bundleID: "com.unknown.app", name: "", enabled: false, running: false }];
    render(<ProxiedAppsList apps={apps} busy={false} onToggle={() => {}} onRemove={() => {}} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
