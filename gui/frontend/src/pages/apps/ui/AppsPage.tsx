import { useStatus } from "@/entities/daemon";
import { LaunchApp } from "@/features/launch-app";
import { useAppTargets, useInstalledApps, useProxiedApps, useRouteApp } from "@/features/route-app";
import { Badge } from "@/shared/ui/badge";
import { Box } from "@/shared/ui/box";
import { Card } from "@/shared/ui/card";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { ApplicationList } from "@/widgets/application-list";
import { InstalledAppsList } from "@/widgets/installed-apps-list";
import { ProxiedAppsList } from "@/widgets/proxied-apps-list";

// AppsPage wires the whole-application data (poll) and actions to the picker
// and the proxied list, plus the launch-through-proxy control. Routing is by
// bundle ID (see internal/procproxy/router_darwin.go): capturing an app covers
// every PID and Electron helper it has, present and future, not just one PID.
export function AppsPage() {
  const status = useStatus();
  const { applications, routed, proxiedApps, refresh } = useAppTargets(status.running);
  const { busy, error, route, unroute } = useRouteApp(refresh);
  const { apps: installedApps, loading: installedLoading, refresh: refreshInstalled } = useInstalledApps();
  const { busy: proxiedBusy, error: proxiedError, launch, setEnabled, remove } = useProxiedApps(refresh);

  return (
    <Stack gap="md">
      <Heading level={1}>Apps</Heading>
      {error && <Text tone="danger" size="sm">{error}</Text>}
      {proxiedError && <Text tone="danger" size="sm">{proxiedError}</Text>}

      <Card>
        <Heading level={3} className="mb-3.5">Launch app through proxy</Heading>
        <LaunchApp onLaunched={refresh} />
      </Card>

      <Card>
        <Heading level={3} className="mb-3.5">Запустить приложение в прокси</Heading>
        <InstalledAppsList
          apps={installedApps}
          loading={installedLoading}
          busy={proxiedBusy}
          onLaunch={launch}
          onRefresh={refreshInstalled}
        />
      </Card>

      <Box className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <Heading level={3} className="mb-3.5">Applications</Heading>
          <ApplicationList applications={applications} routed={routed} busy={busy} onRoute={route} onUnroute={unroute} />
        </Card>
        <Card>
          <Stack direction="row" justify="between" align="center" className="mb-3.5">
            <Heading level={3}>Currently proxied</Heading>
            <Badge>{proxiedApps.length}</Badge>
          </Stack>
          <ProxiedAppsList apps={proxiedApps} busy={proxiedBusy} onToggle={setEnabled} onRemove={remove} />
        </Card>
      </Box>
    </Stack>
  );
}
