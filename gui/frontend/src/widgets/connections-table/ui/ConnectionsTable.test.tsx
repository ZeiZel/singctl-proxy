import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { type TConnRow } from "@/entities/connection";

import { ConnectionsTable } from "./ConnectionsTable";

const ROWS: TConnRow[] = [
  { process: "curl", source: "127.0.0.1:5000", dest: "example.com:443", network: "tcp", chain: "proxy-0" },
  { process: "chrome", source: "127.0.0.1:5001", dest: "google.com:443", network: "tcp", chain: "proxy-0" },
];

describe("ConnectionsTable", () => {
  it("renders rows", () => {
    render(<ConnectionsTable rows={ROWS} />);
    expect(screen.getByText("curl")).toBeInTheDocument();
    expect(screen.getByText("chrome")).toBeInTheDocument();
  });

  it("filters rows and falls back to empty state", async () => {
    render(<ConnectionsTable rows={ROWS} />);
    await userEvent.type(screen.getByLabelText("connections-filter"), "zzz");
    expect(screen.getByText("No active connections.")).toBeInTheDocument();
  });

  it("shows the empty state when there are no connections", () => {
    render(<ConnectionsTable rows={[]} />);
    expect(screen.getByText("No active connections.")).toBeInTheDocument();
  });
});
