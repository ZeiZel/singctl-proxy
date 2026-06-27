import { useLatency } from "@/entities/daemon";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";
import { LatencyList } from "@/widgets/latency-list";

// ProxiesPage shows per-server latency for the failover group.
export function ProxiesPage() {
  const latency = useLatency();
  return (
    <Stack gap="md">
      <Heading level={1}>Proxies</Heading>
      <LatencyList latency={latency} />
    </Stack>
  );
}
