import { useEffect, useMemo, useState } from "react";
import { Live } from "../live";
import { api, ProcInfo } from "../api";

export default function Apps({ live }: { live: Live }) {
  const [procs, setProcs] = useState<ProcInfo[]>([]);
  const [routed, setRouted] = useState<number[]>([]);
  const [filter, setFilter] = useState("");
  const [launchCmd, setLaunchCmd] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const refresh = async () => {
    try {
      const [p, r] = await Promise.all([api.listProcesses(), api.listRouted()]);
      setProcs(p || []);
      setRouted(r || []);
      setErr("");
    } catch (e) {
      setErr("" + e);
    }
  };

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 4000);
    return () => clearInterval(t);
  }, [live.status.running]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q ? procs.filter((p) => p.Name.toLowerCase().includes(q)) : procs;
  }, [procs, filter]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr("");
    try {
      await fn();
      await refresh();
    } catch (e) {
      setErr("" + e);
    } finally {
      setBusy(false);
    }
  };

  const launch = () => {
    const argv = launchCmd.trim().split(/\s+/).filter(Boolean);
    if (argv.length === 0) return;
    act(() => api.launchApp(argv)).then(() => setLaunchCmd(""));
  };

  return (
    <div>
      <h1 className="page-title">Apps</h1>
      {err && <div className="banner" style={{ color: "var(--red)", borderColor: "rgba(255,93,108,0.3)", background: "rgba(255,93,108,0.1)" }}>{err}</div>}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="section-head">
          <h3>Launch app through proxy</h3>
        </div>
        <div className="row">
          <input
            className="field"
            placeholder="e.g. /usr/bin/zen-browser  or  curl https://example.com"
            value={launchCmd}
            onChange={(e) => setLaunchCmd(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && launch()}
          />
          <button className="btn primary" disabled={busy || !launchCmd.trim()} onClick={launch}>
            Launch
          </button>
        </div>
      </div>

      <div className="grid cols-2">
        <div className="card">
          <div className="section-head">
            <h3>Processes</h3>
            <input
              className="field"
              style={{ width: 160 }}
              placeholder="Filter…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          <div style={{ maxHeight: "calc(100vh - 320px)", overflowY: "auto" }}>
            <table className="tbl">
              <thead>
                <tr>
                  <th>App</th>
                  <th>PID</th>
                  <th>Ports</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((p) => (
                  <tr key={p.PID}>
                    <td>
                      {p.Name}
                      {p.Children > 0 && <span style={{ color: "var(--text-faint)" }}> (+{p.Children})</span>}
                    </td>
                    <td className="mono">{p.PID}</td>
                    <td className="mono">{p.Ports}</td>
                    <td style={{ textAlign: "right" }}>
                      <button className="btn sm" disabled={busy} onClick={() => act(() => api.routePID(p.PID))}>
                        Route
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="card">
          <div className="section-head">
            <h3>Currently proxied</h3>
            <span className="badge dim">{routed.length}</span>
          </div>
          {routed.length === 0 ? (
            <div className="empty">No processes routed.</div>
          ) : (
            <table className="tbl">
              <thead>
                <tr>
                  <th>PID</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {routed.map((pid) => (
                  <tr key={pid}>
                    <td className="mono">{pid}</td>
                    <td style={{ textAlign: "right" }}>
                      <button className="btn sm" disabled={busy} onClick={() => act(() => api.unroutePID(pid))}>
                        Unroute
                      </button>{" "}
                      <button className="btn sm danger" disabled={busy} onClick={() => act(() => api.killPID(pid))}>
                        Kill
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
