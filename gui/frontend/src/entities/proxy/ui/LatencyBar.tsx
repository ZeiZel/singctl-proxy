import { cn } from "@/shared/lib/cn";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";
import { type TLatencyRow } from "@/shared/api/singctl";

export interface LatencyBarProps {
  row: TLatencyRow;
  max: number;
}

function delayColorClass(delayMs: number): string {
  if (delayMs <= 0) return "bg-danger";
  if (delayMs < 150) return "bg-ok";
  if (delayMs < 350) return "bg-warn";
  return "bg-danger";
}

// LatencyBar renders one server's latency as a labelled progress bar.
export function LatencyBar({ row, max }: LatencyBarProps) {
  const widthPercent = row.delay > 0 ? Math.min(100, (row.delay / max) * 100) : 100;
  return (
    <Stack direction="row" align="center" gap="md" className="border-b border-border/50 py-2.5 last:border-0">
      <Stack direction="row" align="center" gap="sm" className="w-40 shrink-0">
        {row.selected && <Text tone="ok">●</Text>}
        <Text>{row.tag}</Text>
      </Stack>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-bg-soft">
        <div className={cn("h-full rounded-full", delayColorClass(row.delay))} style={{ width: `${widthPercent}%` }} />
      </div>
      <Text tone="dim" className="w-[70px] text-right">
        {row.delay > 0 ? `${row.delay} ms` : "timeout"}
      </Text>
    </Stack>
  );
}
