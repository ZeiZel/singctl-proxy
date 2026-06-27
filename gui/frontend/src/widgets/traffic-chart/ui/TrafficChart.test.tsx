import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TrafficChart } from "./TrafficChart";

describe("TrafficChart", () => {
  it("shows an empty state with no samples", () => {
    render(<TrafficChart data={[]} />);
    expect(screen.getByText("No traffic yet.")).toBeInTheDocument();
  });

  it("renders rate badges and svg paths from samples", () => {
    const { container } = render(
      <TrafficChart data={[{ up: 1024, down: 2048 }, { up: 512, down: 4096 }]} />
    );
    expect(screen.getByText(/↑/)).toBeInTheDocument();
    expect(screen.getByText(/↓/)).toBeInTheDocument();
    expect(container.querySelectorAll("path").length).toBeGreaterThanOrEqual(4);
  });
});
