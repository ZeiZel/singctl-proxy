import { useState } from "react";
import Sidebar, { Page } from "./components/Sidebar";
import Dashboard from "./pages/Dashboard";
import Proxies from "./pages/Proxies";
import Connections from "./pages/Connections";
import Apps from "./pages/Apps";
import Keys from "./pages/Keys";
import Console from "./pages/Console";
import Settings from "./pages/Settings";
import { useLive } from "./live";
import { api } from "./api";

export default function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const live = useLive();

  const stopDaemon = async () => {
    if (!confirm("Stop the singctl daemon? Proxying will end.")) return;
    try {
      await api.stopDaemon();
    } catch (e) {
      alert("" + e);
    }
  };

  return (
    <div className="app">
      <Sidebar page={page} setPage={setPage} running={live.status.running} />
      <div className="content">
        {page === "dashboard" && <Dashboard live={live} />}
        {page === "proxies" && <Proxies live={live} />}
        {page === "connections" && <Connections live={live} />}
        {page === "apps" && <Apps live={live} />}
        {page === "keys" && <Keys />}
        {page === "console" && <Console live={live} />}
        {page === "settings" && <Settings onStopDaemon={stopDaemon} />}
      </div>
    </div>
  );
}
