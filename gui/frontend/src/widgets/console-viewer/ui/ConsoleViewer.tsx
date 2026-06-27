import { useEffect, useMemo, useRef, useState } from "react";

import { type TConsoleLine } from "@/shared/api/singctl";
import { cn } from "@/shared/lib/cn";
import { EmptyState } from "@/shared/ui/empty-state";
import { Select } from "@/shared/ui/select";
import { Stack } from "@/shared/ui/stack";

export interface ConsoleViewerProps {
  lines: TConsoleLine[];
}

const STREAM_CLASS: Record<string, string> = {
  stderr: "text-warn",
  exit: "text-text-faint",
};

// ConsoleViewer streams captured per-app stdout/stderr with an app filter and
// auto-scroll to the latest line.
export function ConsoleViewer({ lines }: ConsoleViewerProps) {
  const [appFilter, setAppFilter] = useState("");
  const boxRef = useRef<HTMLDivElement>(null);

  const apps = useMemo(() => {
    const seen = new Set<string>();
    lines.forEach((line) => line.app && seen.add(line.app));
    return Array.from(seen);
  }, [lines]);

  const visible = appFilter ? lines.filter((line) => line.app === appFilter) : lines;

  useEffect(() => {
    const node = boxRef.current;
    if (node) {
      node.scrollTop = node.scrollHeight;
    }
  }, [visible.length]);

  return (
    <Stack gap="sm">
      <Select
        aria-label="console-app-filter"
        className="w-52 self-end"
        value={appFilter}
        onChange={(event) => setAppFilter(event.target.value)}
      >
        <option value="">All apps</option>
        {apps.map((app) => (
          <option key={app} value={app}>
            {app}
          </option>
        ))}
      </Select>
      <div
        ref={boxRef}
        className="h-[calc(100vh-200px)] select-text overflow-y-auto rounded-card border border-border bg-[#0c0e13] p-3.5 font-mono text-xs leading-relaxed"
      >
        {visible.length === 0 ? (
          <EmptyState>No app output yet. Launch or route an app from the Apps page.</EmptyState>
        ) : (
          visible.map((line, index) => (
            <div key={index} className={cn("whitespace-pre-wrap break-all", STREAM_CLASS[line.stream])}>
              <span className="text-text-faint">[{line.app || line.pid}] </span>
              {line.text}
            </div>
          ))
        )}
      </div>
    </Stack>
  );
}
