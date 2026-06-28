import { useMemo, useState } from "react";

import { type TProcInfo } from "@/entities/process";
import { Button } from "@/shared/ui/button";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface ProcessListProps {
  processes: TProcInfo[];
  busy: boolean;
  onRoute: (pid: number) => void;
}

// ProcessList is the picker of processes with sockets; each row can be routed.
export function ProcessList({ processes, busy, onRoute }: ProcessListProps) {
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    return query ? processes.filter((process) => process.Name.toLowerCase().includes(query)) : processes;
  }, [processes, filter]);

  return (
    <Stack gap="sm">
      <Input
        aria-label="process-filter"
        className="w-40 self-end"
        placeholder="Filter…"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      {visible.length === 0 ? (
        <EmptyState>No processes with sockets.</EmptyState>
      ) : (
        <div className="max-h-[calc(100vh-320px)] overflow-auto">
          <Table className="whitespace-nowrap">
            <TableHead>
              <TableRow>
                <TableHeaderCell>App</TableHeaderCell>
                <TableHeaderCell>PID</TableHeaderCell>
                <TableHeaderCell>Ports</TableHeaderCell>
                <TableHeaderCell aria-label="actions" />
              </TableRow>
            </TableHead>
            <TableBody>
              {visible.map((process) => (
                <TableRow key={process.PID}>
                  <TableCell>
                    {process.Name}
                    {process.Children > 0 && <Text tone="faint"> (+{process.Children})</Text>}
                  </TableCell>
                  <TableCell className="font-mono">{process.PID}</TableCell>
                  <TableCell className="font-mono">{process.Ports}</TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" disabled={busy} onClick={() => onRoute(process.PID)}>
                      Route
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
