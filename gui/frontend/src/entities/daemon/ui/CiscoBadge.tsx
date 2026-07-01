import { Badge } from "@/shared/ui/badge";
import { type TStatus } from "@/shared/api/singctl";

export interface CiscoBadgeProps {
  status: TStatus;
}

// CiscoBadge surfaces the Cisco-coexistence state. It renders nothing unless a
// corporate VPN (Cisco) is active; then it shows whether the proxy bypasses
// Cisco (egress pinned to the physical NIC) or fell back to riding it.
export function CiscoBadge({ status }: CiscoBadgeProps) {
  if (!status.running || !status.ciscoActive) {
    return null;
  }
  if (status.proxyBypass) {
    return (
      <Badge tone="ok">
        Cisco active · bypass via {status.physIface || "physical NIC"}
      </Badge>
    );
  }
  return <Badge tone="warn">Cisco active · proxy via Cisco (fallback)</Badge>;
}
