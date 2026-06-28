import { NAV_ITEMS, type TPage } from "@/shared/config";
import { cn } from "@/shared/lib/cn";
import { DRAG_REGION, NO_DRAG_REGION } from "@/shared/lib/drag";
import {
  IconApps,
  IconChevronLeft,
  IconConnections,
  IconConsole,
  IconDashboard,
  IconKeys,
  IconProxies,
  IconSettings,
  IconShield,
  type IconProps,
} from "@/shared/ui/icon";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";

export interface SidebarProps {
  page: TPage;
  onNavigate: (page: TPage) => void;
  running: boolean;
  collapsed: boolean;
  onToggleCollapsed: () => void;
}

const ICONS: Record<TPage, (props: IconProps) => JSX.Element> = {
  dashboard: IconDashboard,
  proxies: IconProxies,
  connections: IconConnections,
  apps: IconApps,
  keys: IconKeys,
  console: IconConsole,
  license: IconShield,
  settings: IconSettings,
};

// Sidebar is the primary navigation rail: frosted glass, draggable (the window
// has no title bar), and collapsible to an icon-only rail. The top padding clears
// the macOS traffic-light buttons that float over the top-left.
export function Sidebar({ page, onNavigate, running, collapsed, onToggleCollapsed }: SidebarProps) {
  return (
    <Stack
      gap="md"
      style={DRAG_REGION}
      className={cn(
        "shrink-0 border-r border-border/60 bg-bg-soft/70 px-2 pb-3 pt-8 backdrop-blur-2xl transition-[width] duration-200",
        collapsed ? "w-16" : "w-[210px]"
      )}
    >
      <Stack direction="row" align="center" justify="between" gap="sm" className="px-1.5">
        {!collapsed && (
          <Stack direction="row" align="center" gap="sm">
            <span
              className={cn(
                "h-2.5 w-2.5 rounded-full",
                running ? "bg-ok shadow-[0_0_8px] shadow-ok" : "bg-text-faint"
              )}
            />
            <Text size="lg" weight="bold">singctl</Text>
          </Stack>
        )}
        <button
          type="button"
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          onClick={onToggleCollapsed}
          style={NO_DRAG_REGION}
          className={cn(
            "flex h-8 w-8 items-center justify-center rounded-lg text-text-dim transition-colors hover:bg-panel hover:text-text",
            collapsed && "mx-auto"
          )}
        >
          <IconChevronLeft className={cn("h-[18px] w-[18px] transition-transform", collapsed && "rotate-180")} />
        </button>
      </Stack>

      <Stack gap="none" className="gap-0.5">
        {NAV_ITEMS.map((item) => {
          const Icon = ICONS[item.id];
          const active = page === item.id;
          return (
            <button
              key={item.id}
              type="button"
              title={collapsed ? item.label : undefined}
              onClick={() => onNavigate(item.id)}
              style={NO_DRAG_REGION}
              className={cn(
                "flex items-center gap-3 rounded-[9px] py-2.5 font-medium text-text-dim transition-colors hover:bg-panel hover:text-text",
                collapsed ? "justify-center px-0" : "px-3",
                { "bg-accent/15 text-text": active }
              )}
            >
              <Icon className={cn("h-[18px] w-[18px] shrink-0", { "text-accent": active })} />
              {!collapsed && item.label}
            </button>
          );
        })}
      </Stack>

      {!collapsed && (
        <Text tone="faint" size="xs" className="mt-auto px-2.5">
          {running ? "daemon connected" : "daemon offline"}
        </Text>
      )}
      {collapsed && (
        <span
          title={running ? "daemon connected" : "daemon offline"}
          className={cn("mx-auto mt-auto h-2 w-2 rounded-full", running ? "bg-ok" : "bg-text-faint")}
        />
      )}
    </Stack>
  );
}
