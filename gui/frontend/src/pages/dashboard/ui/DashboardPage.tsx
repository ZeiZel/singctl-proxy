import {
  CiscoBadge,
  StatusBadge,
  useConnections,
  useLatency,
  useStatus,
  useTotalDown,
  useTotalUp,
  useTraffic,
} from "@/entities/daemon";
import { useLicense } from "@/entities/license";
import { ModeSwitch } from "@/features/mode-switch";
import { formatBytes, formatRate } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { Box } from "@/shared/ui/box";
import { Card } from "@/shared/ui/card";
import { EmptyState } from "@/shared/ui/empty-state";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { TrafficChart } from "@/widgets/traffic-chart";

interface StatCardProps {
  label: string;
  value: string;
  tone?: "default" | "accent" | "ok";
}

function StatCard({ label, value, tone = "default" }: StatCardProps) {
  return (
    <Card>
      <Stack gap="xs">
        <Text tone="dim" size="xs" className="uppercase tracking-wide">{label}</Text>
        <Text size="xl" weight="bold" tone={tone}>{value}</Text>
      </Stack>
    </Card>
  );
}

// DashboardPage is the home screen: mode switch, selected node, traffic stats
// and the live traffic chart.
export function DashboardPage() {
  const status = useStatus();
  const traffic = useTraffic();
  const totalUp = useTotalUp();
  const totalDown = useTotalDown();
  const connections = useConnections();
  const latency = useLatency();

  const { info: licenseInfo } = useLicense();

  const lastSample = traffic.at(-1);
  const selected = latency.rows.find((row) => row.selected);

  return (
    <Stack gap="md">
      <Stack direction="row" justify="between" align="center">
        <Heading level={1}>Dashboard</Heading>
        <Stack direction="row" gap="xs" align="center">
          <CiscoBadge status={status} />
          <StatusBadge status={status} />
        </Stack>
      </Stack>

      {licenseInfo.enforced && !licenseInfo.valid && (
        <Card className="border-danger/30 bg-danger/10">
          <Text tone="danger">
            No valid license — open the License section to activate. The proxy service will not start
            until a license is installed.
          </Text>
        </Card>
      )}

      {!status.running && (
        <Card className="border-warn/30 bg-warn/10">
          <Text tone="warn">
            The singctl daemon is not running. Install/start it (make install → LaunchDaemon/systemd) and add a
            key, then this dashboard connects automatically.
          </Text>
        </Card>
      )}

      <Card>
        <Box className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <Stack gap="xs">
            <Text tone="dim" size="xs" className="uppercase tracking-wide">Mode</Text>
            <ModeSwitch mode={status.mode} />
          </Stack>
          <Stack gap="xs" className="sm:items-end">
            <Text tone="dim" size="xs" className="uppercase tracking-wide">Selected node</Text>
            <Text size="lg" weight="semibold">
              {selected ? selected.tag : latency.selected || "—"}
              {selected && (
                <Text tone="dim"> {selected.delay > 0 ? `${selected.delay} ms` : "timeout"}</Text>
              )}
            </Text>
          </Stack>
        </Box>
      </Card>

      <Box className="grid grid-cols-1 gap-4 min-[420px]:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Upload rate" value={lastSample ? formatRate(lastSample.up) : "0 B/s"} tone="accent" />
        <StatCard label="Download rate" value={lastSample ? formatRate(lastSample.down) : "0 B/s"} tone="ok" />
        <StatCard label="Total up" value={formatBytes(totalUp)} />
        <StatCard label="Total down" value={formatBytes(totalDown)} />
      </Box>

      <Card>
        <Stack direction="row" justify="between" align="center" className="mb-3.5">
          <Heading level={3}>Traffic</Heading>
          <Badge>{connections.length} active connections</Badge>
        </Stack>
        {status.clashApi ? (
          <TrafficChart data={traffic} />
        ) : (
          <EmptyState>Enable the Clash API in Settings to see live traffic.</EmptyState>
        )}
      </Card>
    </Stack>
  );
}
