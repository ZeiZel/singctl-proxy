import {
  IconDashboard,
  IconProxies,
  IconConnections,
  IconApps,
  IconKeys,
  IconSettings,
  IconConsole,
} from "./Icons";

export type Page =
  | "dashboard"
  | "proxies"
  | "connections"
  | "apps"
  | "keys"
  | "console"
  | "settings";

const items: { id: Page; label: string; Icon: (p: { className?: string }) => JSX.Element }[] = [
  { id: "dashboard", label: "Dashboard", Icon: IconDashboard },
  { id: "proxies", label: "Proxies", Icon: IconProxies },
  { id: "connections", label: "Connections", Icon: IconConnections },
  { id: "apps", label: "Apps", Icon: IconApps },
  { id: "keys", label: "Keys", Icon: IconKeys },
  { id: "console", label: "Console", Icon: IconConsole },
  { id: "settings", label: "Settings", Icon: IconSettings },
];

interface Props {
  page: Page;
  setPage: (p: Page) => void;
  running: boolean;
}

export default function Sidebar({ page, setPage, running }: Props) {
  return (
    <div className="sidebar">
      <div className="brand">
        <span className={"dot" + (running ? " on" : "")} />
        singctl
      </div>
      <nav className="nav">
        {items.map(({ id, label, Icon }) => (
          <div
            key={id}
            className={"nav-item" + (page === id ? " active" : "")}
            onClick={() => setPage(id)}
          >
            <Icon className="icon" />
            {label}
          </div>
        ))}
      </nav>
      <div className="sidebar-footer">{running ? "daemon connected" : "daemon offline"}</div>
    </div>
  );
}
