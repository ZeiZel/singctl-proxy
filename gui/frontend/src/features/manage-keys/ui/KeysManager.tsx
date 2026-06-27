import { useCallback, useEffect, useState } from "react";

import { api, type TKey } from "@/shared/api/singctl";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "@/shared/ui/table";

// KeysManager lists the loaded VLESS keys (masked) and adds/renames/deletes them.
export function KeysManager() {
  const [keys, setKeys] = useState<TKey[]>([]);
  const [newLink, setNewLink] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    try {
      setKeys((await api.getKeys()) ?? []);
      setError("");
    } catch (caught) {
      setError(String(caught));
    }
  }, []);

  const run = useCallback(
    async (action: () => Promise<unknown>) => {
      setBusy(true);
      setError("");
      try {
        await action();
        await refresh();
      } catch (caught) {
        setError(String(caught));
      } finally {
        setBusy(false);
      }
    },
    [refresh]
  );

  const handleAdd = useCallback(() => {
    const link = newLink.trim();
    if (!link) {
      return;
    }
    run(() => api.addKey(link)).then(() => setNewLink(""));
  }, [newLink, run]);

  const handleRename = useCallback(
    (key: TKey) => {
      const name = window.prompt("New name for the key:", key.name);
      if (name !== null) {
        run(() => api.renameKey(key.index, name));
      }
    },
    [run]
  );

  const handleDelete = useCallback(
    (key: TKey) => {
      if (window.confirm(`Delete key "${key.name}"?`)) {
        run(() => api.deleteKey(key.index));
      }
    },
    [run]
  );

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <Stack gap="md">
      {error && <Text tone="danger" size="sm">{error}</Text>}

      <Card>
        <Heading level={3} className="mb-3.5">Add VLESS key</Heading>
        <Stack direction="row" gap="sm">
          <Input
            aria-label="new-key"
            placeholder="vless://…"
            value={newLink}
            onChange={(event) => setNewLink(event.target.value)}
            onKeyDown={(event) => event.key === "Enter" && handleAdd()}
          />
          <Button variant="primary" disabled={busy || newLink.trim().length === 0} onClick={handleAdd}>
            Add
          </Button>
        </Stack>
        <Text tone="faint" size="xs" className="mt-2 block">
          Multiple keys form an automatic latency-tested failover group (priority follows order).
        </Text>
      </Card>

      <Card>
        <Stack direction="row" justify="between" align="center" className="mb-3.5">
          <Heading level={3}>Loaded keys</Heading>
          <Badge>{keys.length}</Badge>
        </Stack>
        {keys.length === 0 ? (
          <EmptyState>No keys loaded.</EmptyState>
        ) : (
          <Table>
            <TableHead>
              <TableRow>
                <TableHeaderCell>#</TableHeaderCell>
                <TableHeaderCell>Name</TableHeaderCell>
                <TableHeaderCell>Key</TableHeaderCell>
                <TableHeaderCell aria-label="actions" />
              </TableRow>
            </TableHead>
            <TableBody>
              {keys.map((key) => (
                <TableRow key={key.index}>
                  <TableCell className="font-mono">{key.index + 1}</TableCell>
                  <TableCell>{key.name}</TableCell>
                  <TableCell className="font-mono">{key.masked}</TableCell>
                  <TableCell className="text-right">
                    <Stack direction="row" gap="xs" justify="end">
                      <Button size="sm" disabled={busy} onClick={() => handleRename(key)}>Rename</Button>
                      <Button size="sm" variant="danger" disabled={busy} onClick={() => handleDelete(key)}>
                        Delete
                      </Button>
                    </Stack>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </Stack>
  );
}
