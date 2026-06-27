import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { type TConsoleLine } from "@/shared/api/singctl";

import { ConsoleViewer } from "./ConsoleViewer";

const LINES: TConsoleLine[] = [
  { id: 1, pid: 1, app: "zen", stream: "stdout", text: "hello from zen" },
  { id: 2, pid: 2, app: "curl", stream: "stderr", text: "curl warning" },
];

describe("ConsoleViewer", () => {
  it("renders all lines by default", () => {
    render(<ConsoleViewer lines={LINES} />);
    expect(screen.getByText(/hello from zen/)).toBeInTheDocument();
    expect(screen.getByText(/curl warning/)).toBeInTheDocument();
  });

  it("filters by app", async () => {
    render(<ConsoleViewer lines={LINES} />);
    await userEvent.selectOptions(screen.getByLabelText("console-app-filter"), "zen");
    expect(screen.getByText(/hello from zen/)).toBeInTheDocument();
    expect(screen.queryByText(/curl warning/)).not.toBeInTheDocument();
  });

  it("shows an empty state with no lines", () => {
    render(<ConsoleViewer lines={[]} />);
    expect(screen.getByText(/No app output yet/)).toBeInTheDocument();
  });
});
