import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProxiedAppsList } from "./ProxiedAppsList";

describe("ProxiedAppsList", () => {
  it("shows an empty state with nothing routed", () => {
    render(<ProxiedAppsList routed={[]} names={{}} busy={false} onUnroute={() => {}} />);
    expect(screen.getByText("No applications routed.")).toBeInTheDocument();
  });

  it("unroutes the chosen bundle id", async () => {
    const onUnroute = vi.fn();
    render(
      <ProxiedAppsList
        routed={["com.todesktop.cursor"]}
        names={{ "com.todesktop.cursor": "Cursor" }}
        busy={false}
        onUnroute={onUnroute}
      />
    );
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Unroute" }));
    expect(onUnroute).toHaveBeenCalledWith("com.todesktop.cursor");
  });

  it("falls back to a placeholder when the app name is unknown", () => {
    render(<ProxiedAppsList routed={["com.unknown.app"]} names={{}} busy={false} onUnroute={() => {}} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
