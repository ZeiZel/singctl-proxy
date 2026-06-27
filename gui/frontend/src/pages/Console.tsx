import { useEffect, useMemo, useRef, useState } from "react";
import { Live } from "../live";

export default function Console({ live }: { live: Live }) {
  const { console: lines } = live;
  const [appFilter, setAppFilter] = useState("");
  const boxRef = useRef<HTMLDivElement>(null);

  const apps = useMemo(() => {
    const set = new Set<string>();
    lines.forEach((l) => l.app && set.add(l.app));
    return Array.from(set);
  }, [lines]);

  const shown = appFilter ? lines.filter((l) => l.app === appFilter) : lines;

  useEffect(() => {
    const el = boxRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [shown.length]);

  return (
    <div>
      <div className="section-head">
        <h1 className="page-title" style={{ margin: 0 }}>
          Console
        </h1>
        <select className="field" style={{ width: 200 }} value={appFilter} onChange={(e) => setAppFilter(e.target.value)}>
          <option value="">All apps</option>
          {apps.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      </div>
      <div className="console" ref={boxRef}>
        {shown.length === 0 ? (
          <div className="empty">No app output yet. Launch or route an app from the Apps page.</div>
        ) : (
          shown.map((l, i) => (
            <div className={"ln " + l.stream} key={i}>
              <span style={{ color: "var(--text-faint)" }}>[{l.app || l.pid}] </span>
              {l.text}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
