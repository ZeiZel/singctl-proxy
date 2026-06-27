// useLive subscribes to all daemon push-events and exposes them as React state,
// so every page reads the same live snapshot. It keeps a rolling buffer of
// traffic samples for the dashboard chart.
import { useEffect, useState } from "react";
import {
  on,
  api,
  Status,
  TrafficEvent,
  ConnRow,
  Latency,
  ConsoleLine,
} from "./api";

export interface TrafficSample {
  up: number;
  down: number;
}

const MAX_SAMPLES = 60; // ~2 minutes at the 2s poll cadence
const MAX_CONSOLE = 500;

export interface Live {
  status: Status;
  traffic: TrafficSample[]; // rolling rate history (bytes/sec)
  totalUp: number;
  totalDown: number;
  connections: ConnRow[];
  latency: Latency;
  console: ConsoleLine[];
}

export function useLive(): Live {
  const [status, setStatus] = useState<Status>({
    running: false,
    pid: 0,
    mode: "off",
    startedAt: "",
    clashApi: false,
  } as Status);
  const [traffic, setTraffic] = useState<TrafficSample[]>([]);
  const [totalUp, setTotalUp] = useState(0);
  const [totalDown, setTotalDown] = useState(0);
  const [connections, setConnections] = useState<ConnRow[]>([]);
  const [latency, setLatency] = useState<Latency>({ selected: "", rows: [] });
  const [consoleLines, setConsoleLines] = useState<ConsoleLine[]>([]);

  useEffect(() => {
    // Seed status immediately (events also push it on the first poll tick).
    api.getStatus().then(setStatus).catch(() => {});

    const offs = [
      on.status(setStatus),
      on.traffic((t: TrafficEvent) => {
        setTotalUp(t.up);
        setTotalDown(t.down);
        setTraffic((prev) => {
          const next = [...prev, { up: t.upRate, down: t.downRate }];
          return next.length > MAX_SAMPLES ? next.slice(next.length - MAX_SAMPLES) : next;
        });
      }),
      on.connections(setConnections),
      on.latency(setLatency),
      on.console((lines: ConsoleLine[]) => {
        setConsoleLines((prev) => {
          const next = [...prev, ...lines];
          return next.length > MAX_CONSOLE ? next.slice(next.length - MAX_CONSOLE) : next;
        });
      }),
    ];
    return () => offs.forEach((f) => f && f());
  }, []);

  return {
    status,
    traffic,
    totalUp,
    totalDown,
    connections,
    latency,
    console: consoleLines,
  };
}
