import { useEffect, useState } from "react";
import { api, Settings as S } from "../api";

function Toggle({ on, onChange }: { on: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className={"toggle" + (on ? " on" : "")} onClick={() => onChange(!on)}>
      <div className="knob" />
    </div>
  );
}

export default function Settings({ onStopDaemon }: { onStopDaemon: () => void }) {
  const [s, setS] = useState<S | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    api.getSettings().then(setS).catch((e) => setMsg("" + e));
  }, []);

  if (!s) {
    return (
      <div>
        <h1 className="page-title">Settings</h1>
        <div className="card">
          <div className="empty">{msg || "Loading settings… (is the daemon running?)"}</div>
        </div>
      </div>
    );
  }

  const set = <K extends keyof S>(k: K, v: S[K]) => setS({ ...s, [k]: v });

  const apply = async () => {
    setBusy(true);
    setMsg("");
    try {
      await api.applySettings(s);
      setMsg("Applied. The core reloaded with the new settings.");
    } catch (e) {
      setMsg("" + e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <h1 className="page-title">Settings</h1>
      {msg && <div className="banner">{msg}</div>}
      <div className="card" style={{ marginBottom: 16 }}>
        <div className="field-row">
          <span className="label">SOCKS port</span>
          <div className="ctl">
            <input
              className="field"
              type="number"
              value={s.SocksPort}
              onChange={(e) => set("SocksPort", parseInt(e.target.value || "0", 10))}
            />
          </div>
        </div>
        <div className="field-row">
          <span className="label">Clash API</span>
          <Toggle on={s.ClashEnabled} onChange={(v) => set("ClashEnabled", v)} />
        </div>
        <div className="field-row">
          <span className="label">Clash API address</span>
          <div className="ctl">
            <input
              className="field"
              value={s.ClashAddr}
              disabled={!s.ClashEnabled}
              onChange={(e) => set("ClashAddr", e.target.value)}
            />
          </div>
        </div>
        <div className="field-row">
          <span className="label">URLTest URL</span>
          <div className="ctl">
            <input className="field" value={s.URLTestURL} onChange={(e) => set("URLTestURL", e.target.value)} />
          </div>
        </div>
        <div className="field-row">
          <span className="label">URLTest interval</span>
          <div className="ctl">
            <input
              className="field"
              value={s.URLTestInterval}
              placeholder="e.g. 3m"
              onChange={(e) => set("URLTestInterval", e.target.value)}
            />
          </div>
        </div>
        <div className="field-row">
          <span className="label">URLTest tolerance (ms)</span>
          <div className="ctl">
            <input
              className="field"
              type="number"
              value={s.URLTestTolerance}
              onChange={(e) => set("URLTestTolerance", parseInt(e.target.value || "0", 10))}
            />
          </div>
        </div>
        <div className="field-row">
          <span className="label">Save profile to disk</span>
          <Toggle on={s.SaveProfile} onChange={(v) => set("SaveProfile", v)} />
        </div>
      </div>

      <div className="row">
        <button className="btn primary" disabled={busy} onClick={apply}>
          Apply
        </button>
        <div className="spacer" />
        <button className="btn danger" disabled={busy} onClick={onStopDaemon}>
          Stop daemon
        </button>
      </div>
    </div>
  );
}
