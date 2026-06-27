// Typed access layer over the Wails Go bindings + runtime events. The React app
// imports only from here, so the generated-binding paths live in one place.
import * as App from "../wailsjs/go/bridge/App";
import { bridge } from "../wailsjs/go/models";
import { EventsOn } from "../wailsjs/runtime/runtime";

export type Status = bridge.Status;
export type Settings = bridge.Settings;
export type Key = bridge.Key;
export type ProcInfo = bridge.ProcInfo;

// Event payloads (emitted by the Go poller; not part of the bound method types).
export interface TrafficEvent {
  up: number;
  down: number;
  upRate: number;
  downRate: number;
}
export interface ConnRow {
  process: string;
  source: string;
  dest: string;
  network: string;
  chain: string;
}
export interface LatencyRow {
  tag: string;
  delay: number;
  selected: boolean;
}
export interface Latency {
  selected: string;
  rows: LatencyRow[];
}
export interface ConsoleLine {
  id: number;
  pid: number;
  app: string;
  stream: string;
  text: string;
}

export type Mode = "off" | "proxy" | "vpn";

// Bound daemon actions.
export const api = {
  getStatus: () => App.GetStatus(),
  setMode: (m: Mode) => App.SetMode(m),
  getKeys: () => App.GetKeys(),
  addKey: (link: string) => App.AddKey(link),
  renameKey: (i: number, name: string) => App.RenameKey(i, name),
  deleteKey: (i: number) => App.DeleteKey(i),
  getSettings: () => App.GetSettings(),
  applySettings: (s: Settings) => App.ApplySettings(s),
  listProcesses: () => App.ListProcesses(),
  listRouted: () => App.ListRouted(),
  routePID: (pid: number) => App.RoutePID(pid),
  unroutePID: (pid: number) => App.UnroutePID(pid),
  killPID: (pid: number) => App.KillPID(pid),
  restartPID: (pid: number) => App.RestartPID(pid),
  launchApp: (argv: string[]) => App.LaunchApp(argv),
  stopDaemon: () => App.StopDaemon(),
};

// Event subscriptions (return an unsubscribe fn).
export const on = {
  status: (cb: (s: Status) => void) => EventsOn("status", cb),
  traffic: (cb: (t: TrafficEvent) => void) => EventsOn("traffic", cb),
  connections: (cb: (rows: ConnRow[]) => void) => EventsOn("connections", cb),
  latency: (cb: (l: Latency) => void) => EventsOn("latency", cb),
  console: (cb: (lines: ConsoleLine[]) => void) => EventsOn("console", cb),
};

// formatBytes renders a byte count as a human string (B/KB/MB/GB).
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

// formatRate renders a per-second byte rate.
export function formatRate(n: number): string {
  return `${formatBytes(n)}/s`;
}
