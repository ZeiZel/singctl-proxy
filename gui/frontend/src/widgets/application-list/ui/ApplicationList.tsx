import { useMemo, useState } from "react";

import { type TApplication } from "@/entities/application";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface ApplicationListProps {
  applications: TApplication[];
  routed: string[]; // bundle IDs currently routed
  busy: boolean;
  onRoute: (bundleID: string) => void;
  onUnroute: (bundleID: string) => void;
}

// ApplicationList is the picker of running applications, grouped by bundle ID
// (Electron helpers folded in). Routing a row captures the whole app — every
// PID it has now, and any it spawns later — instead of one process.
export function ApplicationList({ applications, routed, busy, onRoute, onUnroute }: ApplicationListProps) {
  const [filter, setFilter] = useState("");
  const routedSet = useMemo(() => new Set(routed), [routed]);

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return applications;
    return applications.filter(
      (app) => app.name.toLowerCase().includes(query) || app.bundleID.toLowerCase().includes(query)
    );
  }, [applications, filter]);

  return (
    <Stack gap="sm">
      <Input
        aria-label="app-filter"
        className="w-40 self-end"
        placeholder="Filter…"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      {visible.length === 0 ? (
        <EmptyState>No running applications found.</EmptyState>
      ) : (
        <div className="max-h-[calc(100vh-320px)] overflow-auto">
          <Table className="whitespace-nowrap">
            <TableHead>
              <TableRow>
                <TableHeaderCell>App</TableHeaderCell>
                <TableHeaderCell>Bundle ID</TableHeaderCell>
                <TableHeaderCell>PIDs</TableHeaderCell>
                <TableHeaderCell aria-label="actions" />
              </TableRow>
            </TableHead>
            <TableBody>
              {visible.map((app) => {
                const isRouted = routedSet.has(app.bundleID);
                return (
                  <TableRow key={app.bundleID}>
                    <TableCell>
                      {app.name}
                      {app.running && <Text tone="faint"> • running</Text>}
                    </TableCell>
                    <TableCell className="font-mono">{app.bundleID}</TableCell>
                    <TableCell className="font-mono">{app.pids.join(", ")}</TableCell>
                    <TableCell className="text-right">
                      {isRouted ? (
                        <Button size="sm" variant="danger" disabled={busy} onClick={() => onUnroute(app.bundleID)}>
                          Unroute
                        </Button>
                      ) : (
                        <Button size="sm" disabled={busy} onClick={() => onRoute(app.bundleID)}>
                          Route
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </Stack>
  );
}
