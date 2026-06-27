import { LatencyBar, type TLatency } from "@/entities/proxy";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { EmptyState } from "@/shared/ui/empty-state";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";

export interface LatencyListProps {
  latency: TLatency;
}

const MIN_SCALE_MS = 300;

// LatencyList renders the failover group's per-server latency bars.
export function LatencyList({ latency }: LatencyListProps) {
  const max = Math.max(MIN_SCALE_MS, ...latency.rows.map((row) => row.delay));

  return (
    <Card>
      <Stack direction="row" justify="between" align="center" className="mb-3.5">
        <Heading level={3}>Failover group (urltest)</Heading>
        <Badge>selected: {latency.selected || "—"}</Badge>
      </Stack>
      {latency.rows.length === 0 ? (
        <EmptyState>No latency data yet. Enable Proxy/VPN mode with the Clash API on.</EmptyState>
      ) : (
        latency.rows.map((row) => <LatencyBar key={row.tag} row={row} max={max} />)
      )}
    </Card>
  );
}
