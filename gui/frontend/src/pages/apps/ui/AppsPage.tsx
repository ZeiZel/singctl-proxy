import { useStatus } from "@/entities/daemon";
import { LaunchApp } from "@/features/launch-app";
import { useProxyTargets, useRouteProcess } from "@/features/route-process";
import { Badge } from "@/shared/ui/badge";
import { Box } from "@/shared/ui/box";
import { Card } from "@/shared/ui/card";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { ProcessList } from "@/widgets/process-list";
import { ProxiedList } from "@/widgets/proxied-list";

// AppsPage wires the per-process data (poll) and actions to the picker and the
// proxied list, plus the launch-through-proxy control.
export function AppsPage() {
  const status = useStatus();
  const { processes, routed, refresh } = useProxyTargets(status.running);
  const { busy, error, route, unroute, kill } = useRouteProcess(refresh);

  return (
    <Stack gap="md">
      <Heading level={1}>Apps</Heading>
      {error && <Text tone="danger" size="sm">{error}</Text>}

      <Card>
        <Heading level={3} className="mb-3.5">Launch app through proxy</Heading>
        <LaunchApp onLaunched={refresh} />
      </Card>

      <Box className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <Heading level={3} className="mb-3.5">Processes</Heading>
          <ProcessList processes={processes} busy={busy} onRoute={route} />
        </Card>
        <Card>
          <Stack direction="row" justify="between" align="center" className="mb-3.5">
            <Heading level={3}>Currently proxied</Heading>
            <Badge>{routed.length}</Badge>
          </Stack>
          <ProxiedList routed={routed} busy={busy} onUnroute={unroute} onKill={kill} />
        </Card>
      </Box>
    </Stack>
  );
}
