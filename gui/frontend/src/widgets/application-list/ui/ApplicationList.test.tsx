import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { type TApplication } from "@/entities/application";

import { ApplicationList } from "./ApplicationList";

const APPLICATIONS: TApplication[] = [
  { name: "Cursor", bundleID: "com.todesktop.cursor", running: true, pids: [100, 200] },
  { name: "Slack", bundleID: "com.tinyspeck.slackmacgap", running: true, pids: [300] },
];

describe("ApplicationList", () => {
  it("routes the chosen bundle id", async () => {
    const onRoute = vi.fn();
    render(<ApplicationList applications={APPLICATIONS} routed={[]} busy={false} onRoute={onRoute} onUnroute={() => {}} />);
    const rows = screen.getAllByRole("button", { name: "Route" });
    await userEvent.click(rows[0]);
    expect(onRoute).toHaveBeenCalledWith("com.todesktop.cursor");
  });

  it("shows unroute for an already-routed app instead of route", async () => {
    const onUnroute = vi.fn();
    render(
      <ApplicationList
        applications={APPLICATIONS}
        routed={["com.todesktop.cursor"]}
        busy={false}
        onRoute={() => {}}
        onUnroute={onUnroute}
      />
    );
    expect(screen.queryAllByRole("button", { name: "Route" })).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "Unroute" }));
    expect(onUnroute).toHaveBeenCalledWith("com.todesktop.cursor");
  });

  it("disables actions while busy", () => {
    render(<ApplicationList applications={APPLICATIONS} routed={[]} busy onRoute={() => {}} onUnroute={() => {}} />);
    expect(screen.getAllByRole("button", { name: "Route" })[0]).toBeDisabled();
  });

  it("shows an empty state with no applications", () => {
    render(<ApplicationList applications={[]} routed={[]} busy={false} onRoute={() => {}} onUnroute={() => {}} />);
    expect(screen.getByText("No running applications found.")).toBeInTheDocument();
  });

  it("filters by name or bundle id", async () => {
    render(<ApplicationList applications={APPLICATIONS} routed={[]} busy={false} onRoute={() => {}} onUnroute={() => {}} />);
    await userEvent.type(screen.getByLabelText("app-filter"), "slack");
    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.queryByText("Cursor")).not.toBeInTheDocument();
  });
});
