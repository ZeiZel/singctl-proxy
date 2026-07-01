import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type TStatus } from "@/shared/api/singctl";

import { CiscoBadge } from "./CiscoBadge";

const base: TStatus = {
  running: true,
  pid: 1,
  mode: "proxy",
  startedAt: "",
  clashApi: false,
  ciscoActive: false,
  proxyBypass: false,
  physIface: "",
};

describe("CiscoBadge", () => {
  it("renders nothing when Cisco is not active", () => {
    const { container } = render(<CiscoBadge status={base} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the bypass interface when the proxy bypasses Cisco", () => {
    render(<CiscoBadge status={{ ...base, ciscoActive: true, proxyBypass: true, physIface: "en0" }} />);
    expect(screen.getByText(/bypass via en0/)).toBeInTheDocument();
  });

  it("shows the fallback state when the proxy rides Cisco", () => {
    render(<CiscoBadge status={{ ...base, ciscoActive: true, proxyBypass: false }} />);
    expect(screen.getByText(/fallback/)).toBeInTheDocument();
  });
});
