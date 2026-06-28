import { type TLicenseInfo } from "@/shared/api/singctl";
import { Badge } from "@/shared/ui/badge";

export interface LicenseStatusBadgeProps {
  info: TLicenseInfo;
}

// LicenseStatusBadge summarizes the license state as a single coloured pill.
export function LicenseStatusBadge({ info }: LicenseStatusBadgeProps) {
  if (!info.enforced) {
    return <Badge tone="neutral">dev build</Badge>;
  }
  if (!info.valid) {
    return <Badge tone="danger">unlicensed</Badge>;
  }
  if (info.daysLeft >= 0 && info.daysLeft <= 14) {
    return <Badge tone="warn">expires in {info.daysLeft}d</Badge>;
  }
  return <Badge tone="ok">licensed</Badge>;
}
