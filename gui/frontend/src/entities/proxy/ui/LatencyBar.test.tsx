import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LatencyBar } from "./LatencyBar";

describe("LatencyBar", () => {
  it("renders the tag and delay in ms", () => {
    render(<LatencyBar row={{ tag: "proxy-0", delay: 42, selected: true }} max={300} />);
    expect(screen.getByText("proxy-0")).toBeInTheDocument();
    expect(screen.getByText("42 ms")).toBeInTheDocument();
  });

  it("shows timeout when the delay is zero", () => {
    render(<LatencyBar row={{ tag: "proxy-1", delay: 0, selected: false }} max={300} />);
    expect(screen.getByText("timeout")).toBeInTheDocument();
  });
});
