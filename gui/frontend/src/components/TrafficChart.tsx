// TrafficChart draws a dependency-free dual-area sparkline of up/down rates,
// in the style of clash-verge's traffic graph. Pure SVG, scales to the buffer.
import { TrafficSample } from "../live";
import { formatRate } from "../api";

interface Props {
  data: TrafficSample[];
  height?: number;
}

function areaPath(values: number[], max: number, w: number, h: number): string {
  if (values.length === 0) return "";
  const n = values.length;
  const step = n > 1 ? w / (n - 1) : w;
  const y = (v: number) => h - (max > 0 ? (v / max) * (h - 6) : 0) - 2;
  let d = `M 0 ${h} `;
  values.forEach((v, i) => {
    d += `L ${(i * step).toFixed(1)} ${y(v).toFixed(1)} `;
  });
  d += `L ${((n - 1) * step).toFixed(1)} ${h} Z`;
  return d;
}

function linePath(values: number[], max: number, w: number, h: number): string {
  if (values.length === 0) return "";
  const n = values.length;
  const step = n > 1 ? w / (n - 1) : w;
  const y = (v: number) => h - (max > 0 ? (v / max) * (h - 6) : 0) - 2;
  return values
    .map((v, i) => `${i === 0 ? "M" : "L"} ${(i * step).toFixed(1)} ${y(v).toFixed(1)}`)
    .join(" ");
}

export default function TrafficChart({ data, height = 180 }: Props) {
  const w = 600;
  const h = height;
  const ups = data.map((d) => d.up);
  const downs = data.map((d) => d.down);
  const max = Math.max(1, ...ups, ...downs);
  const lastUp = ups.length ? ups[ups.length - 1] : 0;
  const lastDown = downs.length ? downs[downs.length - 1] : 0;

  return (
    <div>
      <div className="row" style={{ marginBottom: 10, gap: 20 }}>
        <span className="badge" style={{ background: "rgba(91,140,255,0.15)", color: "var(--accent)" }}>
          ↑ {formatRate(lastUp)}
        </span>
        <span className="badge green">↓ {formatRate(lastDown)}</span>
      </div>
      <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ width: "100%", height }}>
        <defs>
          <linearGradient id="gUp" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#5b8cff" stopOpacity="0.4" />
            <stop offset="100%" stopColor="#5b8cff" stopOpacity="0" />
          </linearGradient>
          <linearGradient id="gDown" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#3ecf8e" stopOpacity="0.4" />
            <stop offset="100%" stopColor="#3ecf8e" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={areaPath(downs, max, w, h)} fill="url(#gDown)" />
        <path d={linePath(downs, max, w, h)} fill="none" stroke="#3ecf8e" strokeWidth="2" />
        <path d={areaPath(ups, max, w, h)} fill="url(#gUp)" />
        <path d={linePath(ups, max, w, h)} fill="none" stroke="#5b8cff" strokeWidth="2" />
      </svg>
    </div>
  );
}
