import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { type TProcInfo } from "@/entities/process";

import { ProcessList } from "./ProcessList";

const PROCESSES: TProcInfo[] = [
  { PID: 100, Name: "zen", Ports: ":443", Children: 2 },
  { PID: 200, Name: "curl", Ports: ":80", Children: 0 },
];

describe("ProcessList", () => {
  it("routes the chosen pid", async () => {
    const onRoute = vi.fn();
    render(<ProcessList processes={PROCESSES} busy={false} onRoute={onRoute} />);
    const rows = screen.getAllByRole("button", { name: "Route" });
    await userEvent.click(rows[0]);
    expect(onRoute).toHaveBeenCalledWith(100);
  });

  it("disables routing while busy", () => {
    render(<ProcessList processes={PROCESSES} busy onRoute={() => {}} />);
    expect(screen.getAllByRole("button", { name: "Route" })[0]).toBeDisabled();
  });

  it("shows an empty state with no processes", () => {
    render(<ProcessList processes={[]} busy={false} onRoute={() => {}} />);
    expect(screen.getByText("No processes with sockets.")).toBeInTheDocument();
  });
});
