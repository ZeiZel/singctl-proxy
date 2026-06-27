import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProxiedList } from "./ProxiedList";

describe("ProxiedList", () => {
  it("shows an empty state with nothing routed", () => {
    render(<ProxiedList routed={[]} busy={false} onUnroute={() => {}} onKill={() => {}} />);
    expect(screen.getByText("No processes routed.")).toBeInTheDocument();
  });

  it("unroutes and kills the chosen pid", async () => {
    const onUnroute = vi.fn();
    const onKill = vi.fn();
    render(<ProxiedList routed={[321]} busy={false} onUnroute={onUnroute} onKill={onKill} />);
    await userEvent.click(screen.getByRole("button", { name: "Unroute" }));
    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    expect(onUnroute).toHaveBeenCalledWith(321);
    expect(onKill).toHaveBeenCalledWith(321);
  });
});
