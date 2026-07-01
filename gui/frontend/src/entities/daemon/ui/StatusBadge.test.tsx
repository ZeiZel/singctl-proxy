import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type TStatus } from "@/shared/api/singctl";

import { StatusBadge } from "./StatusBadge";

const base: TStatus = {
  running: false,
  pid: 0,
  mode: "off",
  startedAt: "",
  clashApi: false,
  ciscoActive: false,
  proxyBypass: false,
  physIface: "",
};

describe("StatusBadge", () => {
  it("shows offline when the daemon is not running", () => {
    render(<StatusBadge status={base} />);
    expect(screen.getByText("daemon offline")).toBeInTheDocument();
  });

  it("shows the PID when running", () => {
    render(<StatusBadge status={{ ...base, running: true, pid: 4242 }} />);
    expect(screen.getByText(/PID 4242/)).toBeInTheDocument();
  });
});
