export type TMode = "off" | "proxy" | "vpn";

export interface TStatus {
  running: boolean;
  pid: number;
  mode: string;
  startedAt: string;
  clashApi: boolean;
  // Cisco-coexistence: when ciscoActive, proxyBypass tells whether the proxy
  // egress is pinned to physIface (the physical NIC) to bypass Cisco, or rides
  // it (fallback).
  ciscoActive: boolean;
  proxyBypass: boolean;
  physIface: string;
}

export interface TSettings {
  SocksPort: number;
  ClashEnabled: boolean;
  ClashAddr: string;
  URLTestURL: string;
  URLTestInterval: string;
  URLTestTolerance: number;
  SaveProfile: boolean;
}

export interface TKey {
  index: number;
  name: string;
  masked: string;
}

export interface TProcInfo {
  PID: number;
  Name: string;
  Ports: string;
  Children: number;
}

// TApplication is one whole application for the Apps tab's whole-app picker,
// grouped by macOS bundle ID (the netext extension's capture key) — routing it
// covers every PID/helper of that app, not just one process.
export interface TApplication {
  name: string;
  bundleID: string;
  running: boolean;
  pids: number[];
}

export interface TConnRow {
  process: string;
  source: string;
  dest: string;
  network: string;
  chain: string;
}

export interface TLatencyRow {
  tag: string;
  delay: number;
  selected: boolean;
}

export interface TLatency {
  selected: string;
  rows: TLatencyRow[];
}

export interface TConsoleLine {
  id: number;
  pid: number;
  app: string;
  stream: string;
  text: string;
}

export interface TLicenseInfo {
  enforced: boolean;
  valid: boolean;
  subject: string;
  expiresAt: number; // unix seconds; 0 = perpetual
  daysLeft: number; // -1 = perpetual
  features: string[];
  reason: string;
}

export interface TTrafficEvent {
  up: number;
  down: number;
  upRate: number;
  downRate: number;
}
