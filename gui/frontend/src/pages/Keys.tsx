import { useEffect, useState } from "react";
import { api, Key } from "../api";

export default function Keys() {
  const [keys, setKeys] = useState<Key[]>([]);
  const [newLink, setNewLink] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const refresh = async () => {
    try {
      setKeys((await api.getKeys()) || []);
      setErr("");
    } catch (e) {
      setErr("" + e);
    }
  };
  useEffect(() => {
    refresh();
  }, []);

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

  const add = () => {
    if (!newLink.trim()) return;
    act(() => api.addKey(newLink.trim())).then(() => setNewLink(""));
  };

  const rename = (k: Key) => {
    const name = prompt("New name for the key:", k.name);
    if (name != null) act(() => api.renameKey(k.index, name));
  };

  const remove = (k: Key) => {
    if (confirm(`Delete key "${k.name}"?`)) act(() => api.deleteKey(k.index));
  };

  return (
    <div>
      <h1 className="page-title">Keys</h1>
      {err && (
        <div className="banner" style={{ color: "var(--red)", borderColor: "rgba(255,93,108,0.3)", background: "rgba(255,93,108,0.1)" }}>
          {err}
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="section-head">
          <h3>Add VLESS key</h3>
        </div>
        <div className="row">
          <input
            className="field"
            placeholder="vless://…"
            value={newLink}
            onChange={(e) => setNewLink(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
          />
          <button className="btn primary" disabled={busy || !newLink.trim()} onClick={add}>
            Add
          </button>
        </div>
        <div style={{ color: "var(--text-faint)", fontSize: 12, marginTop: 8 }}>
          Multiple keys form an automatic latency-tested failover group (priority follows order).
        </div>
      </div>

      <div className="card">
        <div className="section-head">
          <h3>Loaded keys</h3>
          <span className="badge dim">{keys.length}</span>
        </div>
        {keys.length === 0 ? (
          <div className="empty">No keys loaded.</div>
        ) : (
          <table className="tbl">
            <thead>
              <tr>
                <th>#</th>
                <th>Name</th>
                <th>Key</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.index}>
                  <td className="mono">{k.index + 1}</td>
                  <td>{k.name}</td>
                  <td className="mono">{k.masked}</td>
                  <td style={{ textAlign: "right" }}>
                    <button className="btn sm" disabled={busy} onClick={() => rename(k)}>
                      Rename
                    </button>{" "}
                    <button className="btn sm danger" disabled={busy} onClick={() => remove(k)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
