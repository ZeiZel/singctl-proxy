import { useMemo, useState } from "react";

import { type TConnRow } from "@/entities/connection";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

export interface ConnectionsTableProps {
  rows: TConnRow[];
}

// ConnectionsTable shows the live connection list with a process/host filter.
export function ConnectionsTable({ rows }: ConnectionsTableProps) {
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) {
      return rows;
    }
    return rows.filter(
      (row) =>
        row.process.toLowerCase().includes(query) ||
        row.dest.toLowerCase().includes(query) ||
        row.chain.toLowerCase().includes(query)
    );
  }, [rows, filter]);

  return (
    <Stack gap="sm">
      <Input
        aria-label="connections-filter"
        className="w-60 self-end"
        placeholder="Filter by process / host…"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      {visible.length === 0 ? (
        <EmptyState>No active connections.</EmptyState>
      ) : (
        <div className="max-h-[calc(100vh-220px)] overflow-y-auto">
          <Table>
            <TableHead>
              <TableRow>
                <TableHeaderCell>Process</TableHeaderCell>
                <TableHeaderCell>Source</TableHeaderCell>
                <TableHeaderCell>Destination</TableHeaderCell>
                <TableHeaderCell>Net</TableHeaderCell>
                <TableHeaderCell>Chain</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {visible.map((row, index) => (
                <TableRow key={`${row.source}-${index}`}>
                  <TableCell>{row.process || "?"}</TableCell>
                  <TableCell className="font-mono">{row.source}</TableCell>
                  <TableCell className="font-mono">{row.dest}</TableCell>
                  <TableCell>{row.network}</TableCell>
                  <TableCell className="font-mono">{row.chain}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Stack>
  );
}
