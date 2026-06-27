import { EventsOn } from "@wails/runtime/runtime";

import {
  type TConnRow,
  type TConsoleLine,
  type TLatency,
  type TStatus,
  type TTrafficEvent,
} from "./types";

type Unsubscribe = () => void;

// on subscribes to the daemon push-events emitted by the Go poller. Each call
// returns an unsubscribe function.
export const on = {
  status: (callback: (status: TStatus) => void): Unsubscribe => EventsOn("status", callback),
  traffic: (callback: (traffic: TTrafficEvent) => void): Unsubscribe => EventsOn("traffic", callback),
  connections: (callback: (rows: TConnRow[]) => void): Unsubscribe => EventsOn("connections", callback),
  latency: (callback: (latency: TLatency) => void): Unsubscribe => EventsOn("latency", callback),
  console: (callback: (lines: TConsoleLine[]) => void): Unsubscribe => EventsOn("console", callback),
};
