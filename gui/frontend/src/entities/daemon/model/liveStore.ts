import { create } from "zustand";

import {
  api,
  on,
  type TConnRow,
  type TConsoleLine,
  type TLatency,
  type TStatus,
  type TTrafficEvent,
} from "@/shared/api/singctl";

import { type TTrafficSample } from "./types";

const MAX_TRAFFIC_SAMPLES = 60;
const MAX_CONSOLE_LINES = 500;

const INITIAL_STATUS: TStatus = {
  running: false,
  pid: 0,
  mode: "off",
  startedAt: "",
  clashApi: false,
  ciscoActive: false,
  proxyBypass: false,
  physIface: "",
};

interface ILiveState {
  status: TStatus;
  traffic: TTrafficSample[];
  totalUp: number;
  totalDown: number;
  connections: TConnRow[];
  latency: TLatency;
  console: TConsoleLine[];
  setStatus: (status: TStatus) => void;
  pushTraffic: (event: TTrafficEvent) => void;
  setConnections: (rows: TConnRow[]) => void;
  setLatency: (latency: TLatency) => void;
  pushConsole: (lines: TConsoleLine[]) => void;
}

export const useLiveStore = create<ILiveState>((set) => ({
  status: INITIAL_STATUS,
  traffic: [],
  totalUp: 0,
  totalDown: 0,
  connections: [],
  latency: { selected: "", rows: [] },
  console: [],
  setStatus: (status) => set({ status }),
  pushTraffic: (event) =>
    set((state) => {
      const next = [...state.traffic, { up: event.upRate, down: event.downRate }];
      return {
        totalUp: event.up,
        totalDown: event.down,
        traffic: next.length > MAX_TRAFFIC_SAMPLES ? next.slice(next.length - MAX_TRAFFIC_SAMPLES) : next,
      };
    }),
  setConnections: (connections) => set({ connections }),
  setLatency: (latency) => set({ latency }),
  pushConsole: (lines) =>
    set((state) => {
      const next = [...state.console, ...lines];
      return { console: next.length > MAX_CONSOLE_LINES ? next.slice(next.length - MAX_CONSOLE_LINES) : next };
    }),
}));

// initLive seeds the status and subscribes the store to the daemon push-events.
// Call once from the app provider; the returned function unsubscribes.
export function initLive(): () => void {
  const { setStatus, pushTraffic, setConnections, setLatency, pushConsole } = useLiveStore.getState();
  api.getStatus().then(setStatus).catch(() => {});
  const unsubscribers = [
    on.status(setStatus),
    on.traffic(pushTraffic),
    on.connections(setConnections),
    on.latency(setLatency),
    on.console(pushConsole),
  ];
  return () => unsubscribers.forEach((unsubscribe) => unsubscribe?.());
}

export const useStatus = () => useLiveStore((state) => state.status);
export const useTraffic = () => useLiveStore((state) => state.traffic);
export const useTotalUp = () => useLiveStore((state) => state.totalUp);
export const useTotalDown = () => useLiveStore((state) => state.totalDown);
export const useConnections = () => useLiveStore((state) => state.connections);
export const useLatency = () => useLiveStore((state) => state.latency);
export const useConsole = () => useLiveStore((state) => state.console);
