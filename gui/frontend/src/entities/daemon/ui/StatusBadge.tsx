import { Badge } from "@/shared/ui/badge";
import { type TStatus } from "@/shared/api/singctl";

export interface StatusBadgeProps {
  status: TStatus;
}

// StatusBadge shows whether the daemon is running, with its PID.
export function StatusBadge({ status }: StatusBadgeProps) {
  if (!status.running) {
    return <Badge tone="danger">daemon offline</Badge>;
  }
  return <Badge tone="ok">running · PID {status.pid}</Badge>;
}
