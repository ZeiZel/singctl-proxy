import { useConnections } from "@/entities/daemon";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";
import { ConnectionsTable } from "@/widgets/connections-table";

// ConnectionsPage shows the live connection table.
export function ConnectionsPage() {
  const connections = useConnections();
  return (
    <Stack gap="md">
      <Heading level={1}>Connections</Heading>
      <ConnectionsTable rows={connections} />
    </Stack>
  );
}
