import { NAV_ITEMS, type TPage } from "@/shared/config";
import { cn } from "@/shared/lib/cn";
import {
  IconApps,
  IconConnections,
  IconConsole,
  IconDashboard,
  IconKeys,
  IconProxies,
  IconSettings,
  type IconProps,
} from "@/shared/ui/icon";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";

export interface SidebarProps {
  page: TPage;
  onNavigate: (page: TPage) => void;
  running: boolean;
}

const ICONS: Record<TPage, (props: IconProps) => JSX.Element> = {
  dashboard: IconDashboard,
  proxies: IconProxies,
  connections: IconConnections,
  apps: IconApps,
  keys: IconKeys,
  console: IconConsole,
  settings: IconSettings,
};

// Sidebar is the primary navigation rail with a daemon-connection indicator.
export function Sidebar({ page, onNavigate, running }: SidebarProps) {
  return (
    <Stack className="w-[210px] shrink-0 border-r border-border bg-bg-soft p-3" gap="md">
      <Stack direction="row" align="center" gap="sm" className="px-2.5 pb-2 pt-1">
        <span className={cn("h-2.5 w-2.5 rounded-full", running ? "bg-ok shadow-[0_0_8px] shadow-ok" : "bg-text-faint")} />
        <Text size="lg" weight="bold">singctl</Text>
      </Stack>
      <Stack gap="none" className="gap-0.5">
        {NAV_ITEMS.map((item) => {
          const Icon = ICONS[item.id];
          const active = page === item.id;
          return (
            <button
              key={item.id}
              type="button"
              onClick={() => onNavigate(item.id)}
              className={cn(
                "flex items-center gap-3 rounded-[9px] px-3 py-2.5 font-medium text-text-dim transition-colors hover:bg-panel hover:text-text",
                { "bg-accent/15 text-text": active }
              )}
            >
              <Icon className={cn("h-[18px] w-[18px]", { "text-accent": active })} />
              {item.label}
            </button>
          );
        })}
      </Stack>
      <Text tone="faint" size="xs" className="mt-auto px-2.5">
        {running ? "daemon connected" : "daemon offline"}
      </Text>
    </Stack>
  );
}
