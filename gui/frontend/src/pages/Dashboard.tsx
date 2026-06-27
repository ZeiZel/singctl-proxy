import { useState } from "react";
import { Live } from "../live";
import { api, Mode, formatBytes } from "../api";
import TrafficChart from "../components/TrafficChart";

const MODES: Mode[] = ["off", "proxy", "vpn"];
const LABELS: Record<Mode, string> = { off: "Off", proxy: "Proxy", vpn: "VPN" };

export default function Dashboard({ live }: { live: Live }) {
  const { status, traffic, totalUp, totalDown, connections, latency } = live;
  const [busy, setBusy] = useState(false);

  const setMode = async (m: Mode) => {
    setBusy(true);
    try {
      await api.setMode(m);
      live.status.mode = m; // optimistic; the next status event corrects it
    } catch (e) {
      alert("" + e);
    } finally {
      setBusy(false);
    }
  };

  const current = (status.mode as Mode) || "off";
  const selected = latency.rows.find((r) => r.selected);

  return (
    <div>
      <div className="section-head">
        <h1 className="page-title" style={{ margin: 0 }}>
          Dashboard
        </h1>
        <span className={"badge " + (status.running ? "green" : "red")}>
          {status.running ? `running · PID ${status.pid}` : "daemon offline"}
        </span>
      </div>

      {!status.running && (
        <div className="banner">
          The singctl daemon is not running. Install/start it
          (<span className="mono">make install</span> → LaunchDaemon/systemd) and add a key,
          then this dashboard will connect automatically.
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="row" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="stat label">Mode</div>
            <div className="mode-switch" style={{ marginTop: 8 }}>
              {MODES.map((m) => (
                <button
                  key={m}
                  className={"mode-btn " + m + (current === m ? " active" : "")}
                  disabled={busy}
                  onClick={() => setMode(m)}
                >
                  {LABELS[m]}
                </button>
              ))}
            </div>
          </div>
          <div style={{ textAlign: "right" }}>
            <div className="stat label">Selected node</div>
            <div style={{ fontSize: 18, fontWeight: 700, marginTop: 8 }}>
              {selected ? selected.tag : latency.selected || "—"}
              {selected ? (
                <span style={{ color: "var(--text-dim)", fontWeight: 400 }}>
                  {"  "}
                  {selected.delay > 0 ? `${selected.delay} ms` : "timeout"}
                </span>
              ) : null}
            </div>
          </div>
        </div>
      </div>

      <div className="grid cols-4" style={{ marginBottom: 16 }}>
        <div className="card stat">
          <span className="label">Upload rate</span>
          <span className="value up">
            {traffic.length ? formatBytes(traffic[traffic.length - 1].up) + "/s" : "0 B/s"}
          </span>
        </div>
        <div className="card stat">
          <span className="label">Download rate</span>
          <span className="value down">
            {traffic.length ? formatBytes(traffic[traffic.length - 1].down) + "/s" : "0 B/s"}
          </span>
        </div>
        <div className="card stat">
          <span className="label">Total up</span>
          <span className="value">{formatBytes(totalUp)}</span>
        </div>
        <div className="card stat">
          <span className="label">Total down</span>
          <span className="value">{formatBytes(totalDown)}</span>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="section-head">
          <h3>Traffic</h3>
          <span className="badge dim">{connections.length} active connections</span>
        </div>
        {status.clashApi ? (
          <TrafficChart data={traffic} />
        ) : (
          <div className="empty">Enable the Clash API in Settings to see live traffic.</div>
        )}
      </div>
    </div>
  );
}
