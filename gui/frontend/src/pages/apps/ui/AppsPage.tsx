import { useMemo } from "react";

import { useStatus } from "@/entities/daemon";
import { LaunchApp } from "@/features/launch-app";
import { useAppTargets, useRouteApp } from "@/features/route-app";
import { Badge } from "@/shared/ui/badge";
import { Box } from "@/shared/ui/box";
import { Card } from "@/shared/ui/card";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { ApplicationList } from "@/widgets/application-list";
import { ProxiedAppsList } from "@/widgets/proxied-apps-list";

// AppsPage wires the whole-application data (poll) and actions to the picker
// and the proxied list, plus the launch-through-proxy control. Routing is by
// bundle ID (see internal/procproxy/router_darwin.go): capturing an app covers
// every PID and Electron helper it has, present and future, not just one PID.
export function AppsPage() {
  const status = useStatus();
  const { applications, routed, refresh } = useAppTargets(status.running);
  const { busy, error, route, unroute } = useRouteApp(refresh);

  const names = useMemo(() => {
    const map: Record<string, string> = {};
    for (const app of applications) map[app.bundleID] = app.name;
    return map;
  }, [applications]);

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
          <Heading level={3} className="mb-3.5">Applications</Heading>
          <ApplicationList applications={applications} routed={routed} busy={busy} onRoute={route} onUnroute={unroute} />
        </Card>
        <Card>
          <Stack direction="row" justify="between" align="center" className="mb-3.5">
            <Heading level={3}>Currently proxied</Heading>
            <Badge>{routed.length}</Badge>
          </Stack>
          <ProxiedAppsList routed={routed} names={names} busy={busy} onUnroute={unroute} />
        </Card>
      </Box>
    </Stack>
  );
}
