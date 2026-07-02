import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface ProxiedAppsListProps {
  routed: string[]; // bundle IDs currently routed
  names: Record<string, string>; // bundle ID -> display name, when known
  busy: boolean;
  onUnroute: (bundleID: string) => void;
}

// ProxiedAppsList shows the applications currently routed through the proxy
// (by bundle ID — every PID and Electron helper of that app, present and
// future) with a per-row unroute action.
export function ProxiedAppsList({ routed, names, busy, onUnroute }: ProxiedAppsListProps) {
  if (routed.length === 0) {
    return <EmptyState>No applications routed.</EmptyState>;
  }
  return (
    <div className="overflow-x-auto">
      <Table className="whitespace-nowrap">
        <TableHead>
          <TableRow>
            <TableHeaderCell>App</TableHeaderCell>
            <TableHeaderCell>Bundle ID</TableHeaderCell>
            <TableHeaderCell aria-label="actions" />
          </TableRow>
        </TableHead>
        <TableBody>
          {routed.map((bundleID) => (
            <TableRow key={bundleID}>
              <TableCell>{names[bundleID] ?? "—"}</TableCell>
              <TableCell className="font-mono">{bundleID}</TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="danger" disabled={busy} onClick={() => onUnroute(bundleID)}>
                  Unroute
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export function ProxiedAppsCount({ count }: { count: number }) {
  return <Badge>{count}</Badge>;
}
