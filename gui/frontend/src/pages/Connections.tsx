import { useMemo, useState } from "react";
import { Live } from "../live";

export default function Connections({ live }: { live: Live }) {
  const { connections } = live;
  const [filter, setFilter] = useState("");

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return connections;
    return connections.filter(
      (c) =>
        c.process.toLowerCase().includes(q) ||
        c.dest.toLowerCase().includes(q) ||
        c.chain.toLowerCase().includes(q)
    );
  }, [connections, filter]);

  return (
    <div>
      <div className="section-head">
        <h1 className="page-title" style={{ margin: 0 }}>
          Connections
        </h1>
        <input
          className="field"
          style={{ width: 240 }}
          placeholder="Filter by process / host…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {rows.length === 0 ? (
          <div className="empty">No active connections.</div>
        ) : (
          <div style={{ maxHeight: "calc(100vh - 200px)", overflowY: "auto" }}>
            <table className="tbl">
              <thead>
                <tr>
                  <th>Process</th>
                  <th>Source</th>
                  <th>Destination</th>
                  <th>Net</th>
                  <th>Chain</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((c, i) => (
                  <tr key={i}>
                    <td>{c.process || "?"}</td>
                    <td className="mono">{c.source}</td>
                    <td className="mono">{c.dest}</td>
                    <td>{c.network}</td>
                    <td className="mono">{c.chain}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
