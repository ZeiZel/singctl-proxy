import { type TProxiedApp } from "@/shared/api/singctl";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";
import { Text } from "@/shared/ui/text";
import { Toggle } from "@/shared/ui/toggle";

export interface ProxiedAppsListProps {
  apps: TProxiedApp[];
  busy: boolean;
  onToggle: (bundleID: string, enabled: boolean) => void;
  onRemove: (bundleID: string) => void;
}

// ProxiedAppsList shows the applications currently in the proxy's app list
// (by bundle ID — every PID and Electron helper of that app, present and
// future) with a per-row enable/disable toggle and a remove action.
export function ProxiedAppsList({ apps, busy, onToggle, onRemove }: ProxiedAppsListProps) {
  if (apps.length === 0) {
    return <EmptyState>No applications routed.</EmptyState>;
  }
  return (
    <div className="overflow-x-auto">
      <Table className="whitespace-nowrap">
        <TableHead>
          <TableRow>
            <TableHeaderCell>App</TableHeaderCell>
            <TableHeaderCell>Bundle ID</TableHeaderCell>
            <TableHeaderCell aria-label="enabled" />
            <TableHeaderCell aria-label="actions" />
          </TableRow>
        </TableHead>
        <TableBody>
          {apps.map((app) => (
            <TableRow key={app.bundleID}>
              <TableCell>
                {app.name || "—"}
                {app.running && <Text tone="faint"> • running</Text>}
              </TableCell>
              <TableCell className="font-mono">{app.bundleID}</TableCell>
              <TableCell>
                <Toggle
                  checked={app.enabled}
                  disabled={busy}
                  aria-label={`toggle-${app.bundleID}`}
                  onChange={(next) => onToggle(app.bundleID, next)}
                />
              </TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="danger" disabled={busy} onClick={() => onRemove(app.bundleID)}>
                  Удалить
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
