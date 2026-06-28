import { useCallback, useState } from "react";

import { useStatus } from "@/entities/daemon";
import { type TPage } from "@/shared/config";
import { DRAG_REGION } from "@/shared/lib/drag";
import { Box } from "@/shared/ui/box";
import { AppsPage } from "@/pages/apps";
import { ConnectionsPage } from "@/pages/connections";
import { ConsolePage } from "@/pages/console";
import { DashboardPage } from "@/pages/dashboard";
import { KeysPage } from "@/pages/keys";
import { LicensePage } from "@/pages/license";
import { ProxiesPage } from "@/pages/proxies";
import { SettingsPage } from "@/pages/settings";
import { Sidebar } from "@/widgets/app-sidebar";

import { LiveProvider } from "./providers/LiveProvider";
import { PlatformProvider } from "./providers/PlatformProvider";

const PAGES: Record<TPage, () => JSX.Element> = {
  dashboard: DashboardPage,
  proxies: ProxiesPage,
  connections: ConnectionsPage,
  apps: AppsPage,
  keys: KeysPage,
  console: ConsolePage,
  license: LicensePage,
  settings: SettingsPage,
};

const COLLAPSE_KEY = "singctl.sidebar.collapsed";

function Shell() {
  const [page, setPage] = useState<TPage>("dashboard");
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_KEY) === "1");
  const status = useStatus();

  const toggleCollapsed = useCallback(() => {
    setCollapsed((previous) => {
      const next = !previous;
      localStorage.setItem(COLLAPSE_KEY, next ? "1" : "0");
      return next;
    });
  }, []);

  const Page = PAGES[page];

  return (
    <Box className="flex h-screen overflow-hidden text-text">
      <Sidebar
        page={page}
        onNavigate={setPage}
        running={status.running}
        collapsed={collapsed}
        onToggleCollapsed={toggleCollapsed}
      />
      <Box className="flex min-w-0 flex-1 flex-col overflow-hidden">
        {/* Invisible draggable strip standing in for the removed title bar. */}
        <Box className="h-7 shrink-0" style={DRAG_REGION} />
        <Box className="min-w-0 flex-1 overflow-y-auto px-4 pb-6 sm:px-6 lg:px-8">
          <Page />
        </Box>
      </Box>
    </Box>
  );
}

// App is the composition root: platform detection + live store around the shell.
export function App() {
  return (
    <PlatformProvider>
      <LiveProvider>
        <Shell />
      </LiveProvider>
    </PlatformProvider>
  );
}
