import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Sidebar } from "./Sidebar";

const baseProps = {
  page: "dashboard" as const,
  onNavigate: () => {},
  running: true,
  collapsed: false,
  onToggleCollapsed: () => {},
};

describe("Sidebar", () => {
  it("renders all navigation items when expanded", () => {
    render(<Sidebar {...baseProps} />);
    expect(screen.getByRole("button", { name: /Dashboard/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Settings/ })).toBeInTheDocument();
  });

  it("navigates on click", async () => {
    const onNavigate = vi.fn();
    render(<Sidebar {...baseProps} running={false} onNavigate={onNavigate} />);
    await userEvent.click(screen.getByRole("button", { name: /Keys/ }));
    expect(onNavigate).toHaveBeenCalledWith("keys");
  });

  it("hides the brand label and footer text when collapsed", () => {
    render(<Sidebar {...baseProps} collapsed />);
    expect(screen.queryByText("singctl")).not.toBeInTheDocument();
    expect(screen.queryByText("daemon connected")).not.toBeInTheDocument();
    // Nav buttons remain (icon-only).
    expect(screen.getByRole("button", { name: /Dashboard/ })).toBeInTheDocument();
  });

  it("toggles collapse via the chevron button", async () => {
    const onToggleCollapsed = vi.fn();
    render(<Sidebar {...baseProps} onToggleCollapsed={onToggleCollapsed} />);
    await userEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(onToggleCollapsed).toHaveBeenCalledOnce();
  });

  it("offers an expand affordance when collapsed", () => {
    render(<Sidebar {...baseProps} collapsed />);
    expect(screen.getByRole("button", { name: "Expand sidebar" })).toBeInTheDocument();
  });
});
