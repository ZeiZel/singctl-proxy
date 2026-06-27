import { type TTrafficSample } from "@/entities/daemon";
import { formatRate } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { EmptyState } from "@/shared/ui/empty-state";
import { Stack } from "@/shared/ui/stack";

export interface TrafficChartProps {
  data: TTrafficSample[];
  height?: number;
}

const VIEWPORT_WIDTH = 600;
const DEFAULT_HEIGHT = 180;

function pointY(value: number, max: number, height: number): number {
  const usable = height - 6;
  return height - (max > 0 ? (value / max) * usable : 0) - 2;
}

function areaPath(values: number[], max: number, width: number, height: number): string {
  if (values.length === 0) return "";
  const step = values.length > 1 ? width / (values.length - 1) : width;
  const head = `M 0 ${height} `;
  const body = values.map((value, index) => `L ${(index * step).toFixed(1)} ${pointY(value, max, height).toFixed(1)}`).join(" ");
  return `${head}${body} L ${((values.length - 1) * step).toFixed(1)} ${height} Z`;
}

function linePath(values: number[], max: number, width: number, height: number): string {
  if (values.length === 0) return "";
  const step = values.length > 1 ? width / (values.length - 1) : width;
  return values
    .map((value, index) => `${index === 0 ? "M" : "L"} ${(index * step).toFixed(1)} ${pointY(value, max, height).toFixed(1)}`)
    .join(" ");
}

// TrafficChart renders a dependency-free dual-area sparkline of up/down rates.
export function TrafficChart({ data, height = DEFAULT_HEIGHT }: TrafficChartProps) {
  if (data.length === 0) {
    return <EmptyState>No traffic yet.</EmptyState>;
  }
  const ups = data.map((sample) => sample.up);
  const downs = data.map((sample) => sample.down);
  const max = Math.max(1, ...ups, ...downs);

  return (
    <Stack gap="sm">
      <Stack direction="row" gap="lg">
        <Badge tone="accent">↑ {formatRate(ups[ups.length - 1])}</Badge>
        <Badge tone="ok">↓ {formatRate(downs[downs.length - 1])}</Badge>
      </Stack>
      <svg viewBox={`0 0 ${VIEWPORT_WIDTH} ${height}`} preserveAspectRatio="none" className="w-full" style={{ height }}>
        <defs>
          <linearGradient id="trafficUp" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#5b8cff" stopOpacity="0.4" />
            <stop offset="100%" stopColor="#5b8cff" stopOpacity="0" />
          </linearGradient>
          <linearGradient id="trafficDown" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#3ecf8e" stopOpacity="0.4" />
            <stop offset="100%" stopColor="#3ecf8e" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={areaPath(downs, max, VIEWPORT_WIDTH, height)} fill="url(#trafficDown)" />
        <path d={linePath(downs, max, VIEWPORT_WIDTH, height)} fill="none" stroke="#3ecf8e" strokeWidth="2" />
        <path d={areaPath(ups, max, VIEWPORT_WIDTH, height)} fill="url(#trafficUp)" />
        <path d={linePath(ups, max, VIEWPORT_WIDTH, height)} fill="none" stroke="#5b8cff" strokeWidth="2" />
      </svg>
    </Stack>
  );
}
