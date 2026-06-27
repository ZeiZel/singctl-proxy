import { useState } from "react";

import { useStatus } from "@/entities/daemon";
import { type TPage } from "@/shared/config";
import { Box } from "@/shared/ui/box";
import { AppsPage } from "@/pages/apps";
import { ConnectionsPage } from "@/pages/connections";
import { ConsolePage } from "@/pages/console";
import { DashboardPage } from "@/pages/dashboard";
import { KeysPage } from "@/pages/keys";
import { ProxiesPage } from "@/pages/proxies";
import { SettingsPage } from "@/pages/settings";
import { Sidebar } from "@/widgets/app-sidebar";

import { LiveProvider } from "./providers/LiveProvider";

const PAGES: Record<TPage, () => JSX.Element> = {
  dashboard: DashboardPage,
  proxies: ProxiesPage,
  connections: ConnectionsPage,
  apps: AppsPage,
  keys: KeysPage,
  console: ConsolePage,
  settings: SettingsPage,
};

function Shell() {
  const [page, setPage] = useState<TPage>("dashboard");
  const status = useStatus();
  const Page = PAGES[page];

  return (
    <Box className="flex h-screen overflow-hidden bg-bg text-text">
      <Sidebar page={page} onNavigate={setPage} running={status.running} />
      <Box className="flex-1 overflow-y-auto px-8 py-7">
        <Page />
      </Box>
    </Box>
  );
}

// App is the composition root: it wires the live store provider around the shell.
export function App() {
  return (
    <LiveProvider>
      <Shell />
    </LiveProvider>
  );
}
