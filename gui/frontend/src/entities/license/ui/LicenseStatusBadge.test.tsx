import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type TLicenseInfo } from "@/shared/api/singctl";

import { LicenseStatusBadge } from "./LicenseStatusBadge";

const base: TLicenseInfo = {
  enforced: true,
  valid: true,
  subject: "a@b.c",
  expiresAt: 0,
  daysLeft: -1,
  features: [],
  reason: "",
};

describe("LicenseStatusBadge", () => {
  it("shows dev build when enforcement is off", () => {
    render(<LicenseStatusBadge info={{ ...base, enforced: false }} />);
    expect(screen.getByText("dev build")).toBeInTheDocument();
  });

  it("shows unlicensed when invalid", () => {
    render(<LicenseStatusBadge info={{ ...base, valid: false, reason: "no license" }} />);
    expect(screen.getByText("unlicensed")).toBeInTheDocument();
  });

  it("warns when expiring soon", () => {
    render(<LicenseStatusBadge info={{ ...base, daysLeft: 7 }} />);
    expect(screen.getByText("expires in 7d")).toBeInTheDocument();
  });

  it("shows licensed when valid and not near expiry", () => {
    render(<LicenseStatusBadge info={base} />);
    expect(screen.getByText("licensed")).toBeInTheDocument();
  });
});
