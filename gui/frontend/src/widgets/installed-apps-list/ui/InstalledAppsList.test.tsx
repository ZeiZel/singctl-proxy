import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { type TInstalledApp } from "@/shared/api/singctl";

import { InstalledAppsList } from "./InstalledAppsList";

const APPS: TInstalledApp[] = [
  { name: "Cursor", bundleID: "com.todesktop.cursor", path: "/Applications/Cursor.app" },
  { name: "Slack", bundleID: "com.tinyspeck.slackmacgap", path: "/Applications/Slack.app" },
];

describe("InstalledAppsList", () => {
  it("launches the chosen app bundle", async () => {
    const onLaunch = vi.fn();
    render(<InstalledAppsList apps={APPS} loading={false} busy={false} onLaunch={onLaunch} onRefresh={() => {}} />);
    const rows = screen.getAllByRole("button", { name: "Запустить в прокси" });
    await userEvent.click(rows[0]);
    expect(onLaunch).toHaveBeenCalledWith("/Applications/Cursor.app");
  });

  it("disables actions while busy", () => {
    render(<InstalledAppsList apps={APPS} loading={false} busy onLaunch={() => {}} onRefresh={() => {}} />);
    expect(screen.getAllByRole("button", { name: "Запустить в прокси" })[0]).toBeDisabled();
  });

  it("shows an empty state with no applications", () => {
    render(<InstalledAppsList apps={[]} loading={false} busy={false} onLaunch={() => {}} onRefresh={() => {}} />);
    expect(screen.getByText("No installed applications found.")).toBeInTheDocument();
  });

  it("shows a scanning placeholder while loading with nothing yet", () => {
    render(<InstalledAppsList apps={[]} loading busy={false} onLaunch={() => {}} onRefresh={() => {}} />);
    expect(screen.getByText("Scanning installed applications…")).toBeInTheDocument();
  });

  it("filters by name or bundle id", async () => {
    render(<InstalledAppsList apps={APPS} loading={false} busy={false} onLaunch={() => {}} onRefresh={() => {}} />);
    await userEvent.type(screen.getByLabelText("installed-app-filter"), "slack");
    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.queryByText("Cursor")).not.toBeInTheDocument();
  });

  it("triggers a manual refresh", async () => {
    const onRefresh = vi.fn();
    render(<InstalledAppsList apps={APPS} loading={false} busy={false} onLaunch={() => {}} onRefresh={onRefresh} />);
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(onRefresh).toHaveBeenCalled();
  });
});
