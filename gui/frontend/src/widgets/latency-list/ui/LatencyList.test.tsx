import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LatencyList } from "./LatencyList";

describe("LatencyList", () => {
  it("shows an empty state without rows", () => {
    render(<LatencyList latency={{ selected: "", rows: [] }} />);
    expect(screen.getByText(/No latency data/)).toBeInTheDocument();
  });

  it("renders a bar per server", () => {
    render(
      <LatencyList
        latency={{
          selected: "proxy-0",
          rows: [
            { tag: "proxy-0", delay: 42, selected: true },
            { tag: "proxy-1", delay: 0, selected: false },
          ],
        }}
      />
    );
    expect(screen.getByText("proxy-0")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
  });
});
