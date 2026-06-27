import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Stack } from "@/shared/ui/stack";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface ProxiedListProps {
  routed: number[];
  busy: boolean;
  onUnroute: (pid: number) => void;
  onKill: (pid: number) => void;
}

// ProxiedList shows the PIDs currently routed through the proxy with per-row
// unroute/kill actions.
export function ProxiedList({ routed, busy, onUnroute, onKill }: ProxiedListProps) {
  if (routed.length === 0) {
    return <EmptyState>No processes routed.</EmptyState>;
  }
  return (
    <Table>
      <TableHead>
        <TableRow>
          <TableHeaderCell>PID</TableHeaderCell>
          <TableHeaderCell aria-label="actions" />
        </TableRow>
      </TableHead>
      <TableBody>
        {routed.map((pid) => (
          <TableRow key={pid}>
            <TableCell className="font-mono">{pid}</TableCell>
            <TableCell className="text-right">
              <Stack direction="row" gap="xs" justify="end">
                <Button size="sm" disabled={busy} onClick={() => onUnroute(pid)}>
                  Unroute
                </Button>
                <Button size="sm" variant="danger" disabled={busy} onClick={() => onKill(pid)}>
                  Kill
                </Button>
              </Stack>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function ProxiedCount({ count }: { count: number }) {
  return <Badge>{count}</Badge>;
}
