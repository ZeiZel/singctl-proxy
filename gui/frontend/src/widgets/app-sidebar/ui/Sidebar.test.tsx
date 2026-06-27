import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Sidebar } from "./Sidebar";

describe("Sidebar", () => {
  it("renders all navigation items", () => {
    render(<Sidebar page="dashboard" onNavigate={() => {}} running />);
    expect(screen.getByRole("button", { name: /Dashboard/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Settings/ })).toBeInTheDocument();
  });

  it("navigates on click", async () => {
    const onNavigate = vi.fn();
    render(<Sidebar page="dashboard" onNavigate={onNavigate} running={false} />);
    await userEvent.click(screen.getByRole("button", { name: /Keys/ }));
    expect(onNavigate).toHaveBeenCalledWith("keys");
  });

  it("reflects the offline state", () => {
    render(<Sidebar page="dashboard" onNavigate={() => {}} running={false} />);
    expect(screen.getByText("daemon offline")).toBeInTheDocument();
  });
});
