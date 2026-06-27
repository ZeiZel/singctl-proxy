export type TMode = "off" | "proxy" | "vpn";

export interface TStatus {
  running: boolean;
  pid: number;
  mode: string;
  startedAt: string;
  clashApi: boolean;
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

export interface TTrafficEvent {
  up: number;
  down: number;
  upRate: number;
  downRate: number;
}
