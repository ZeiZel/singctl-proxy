import { Live } from "../live";

function delayColor(ms: number): string {
  if (ms <= 0) return "var(--red)";
  if (ms < 150) return "var(--green)";
  if (ms < 350) return "var(--yellow)";
  return "var(--red)";
}

export default function Proxies({ live }: { live: Live }) {
  const { latency } = live;
  const max = Math.max(300, ...latency.rows.map((r) => r.delay));

  return (
    <div>
      <h1 className="page-title">Proxies</h1>
      <div className="card">
        <div className="section-head">
          <h3>Failover group (urltest)</h3>
          <span className="badge dim">selected: {latency.selected || "—"}</span>
        </div>
        {latency.rows.length === 0 ? (
          <div className="empty">No latency data yet. Enable Proxy/VPN mode with the Clash API on.</div>
        ) : (
          latency.rows.map((r) => (
            <div className="lat-row" key={r.tag}>
              <div className="lat-name">
                {r.selected && <span style={{ color: "var(--green)" }}>●</span>}
                <span>{r.tag}</span>
              </div>
              <div className="lat-track">
                <div
                  className="lat-fill"
                  style={{
                    width: `${r.delay > 0 ? Math.min(100, (r.delay / max) * 100) : 100}%`,
                    background: delayColor(r.delay),
                  }}
                />
              </div>
              <div className="lat-ms">{r.delay > 0 ? `${r.delay} ms` : "timeout"}</div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
