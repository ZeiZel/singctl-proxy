export type TPage =
  | "dashboard"
  | "proxies"
  | "connections"
  | "apps"
  | "keys"
  | "console"
  | "license"
  | "settings";

export interface TNavItem {
  id: TPage;
  label: string;
}

export const NAV_ITEMS: TNavItem[] = [
  { id: "dashboard", label: "Dashboard" },
  { id: "proxies", label: "Proxies" },
  { id: "connections", label: "Connections" },
  { id: "apps", label: "Apps" },
  { id: "keys", label: "Keys" },
  { id: "console", label: "Console" },
  { id: "license", label: "License" },
  { id: "settings", label: "Settings" },
];
