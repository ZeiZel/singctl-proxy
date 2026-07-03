import { useMemo, useState } from "react";

import { type TInstalledApp } from "@/shared/api/singctl";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface InstalledAppsListProps {
  apps: TInstalledApp[];
  loading: boolean;
  busy: boolean;
  onLaunch: (path: string) => void;
  onRefresh: () => void;
}

// InstalledAppsList is the searchable picker of every app bundle found on
// disk, for launching one through the proxy. Enumeration is slow-ish (100s of
// apps), so it loads once and only re-scans on an explicit refresh; filtering
// itself happens client-side.
export function InstalledAppsList({ apps, loading, busy, onLaunch, onRefresh }: InstalledAppsListProps) {
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return apps;
    return apps.filter(
      (app) => app.name.toLowerCase().includes(query) || app.bundleID.toLowerCase().includes(query)
    );
  }, [apps, filter]);

  return (
    <Stack gap="sm">
      <Stack direction="row" gap="sm" justify="between" align="center">
        <Input
          aria-label="installed-app-filter"
          className="w-40"
          placeholder="Filter…"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        />
        <Button size="sm" disabled={loading} onClick={onRefresh}>
          {loading ? "Scanning…" : "Refresh"}
        </Button>
      </Stack>
      {loading && apps.length === 0 ? (
        <EmptyState>Scanning installed applications…</EmptyState>
      ) : visible.length === 0 ? (
        <EmptyState>No installed applications found.</EmptyState>
      ) : (
        <div className="max-h-[calc(100vh-320px)] overflow-auto">
          <Table className="whitespace-nowrap">
            <TableHead>
              <TableRow>
                <TableHeaderCell>App</TableHeaderCell>
                <TableHeaderCell>Bundle ID</TableHeaderCell>
                <TableHeaderCell aria-label="actions" />
              </TableRow>
            </TableHead>
            <TableBody>
              {visible.map((app) => (
                <TableRow key={app.bundleID}>
                  <TableCell>{app.name}</TableCell>
                  <TableCell className="font-mono">{app.bundleID}</TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="primary" disabled={busy} onClick={() => onLaunch(app.path)}>
                      Запустить в прокси
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Stack>
  );
}
