import { act } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { type TStatus } from "@/shared/api/singctl";

const handlers: Record<string, (payload: unknown) => void> = {};

vi.mock("@/shared/api/singctl", () => ({
  api: { getStatus: vi.fn().mockResolvedValue({ running: false, pid: 0, mode: "off" } as TStatus) },
  on: {
    status: (callback: (payload: unknown) => void) => {
      handlers.status = callback;
      return () => delete handlers.status;
    },
    traffic: (callback: (payload: unknown) => void) => {
      handlers.traffic = callback;
      return () => delete handlers.traffic;
    },
    connections: (callback: (payload: unknown) => void) => {
      handlers.connections = callback;
      return () => delete handlers.connections;
    },
    latency: (callback: (payload: unknown) => void) => {
      handlers.latency = callback;
      return () => delete handlers.latency;
    },
    console: (callback: (payload: unknown) => void) => {
      handlers.console = callback;
      return () => delete handlers.console;
    },
  },
}));

import { initLive, useLiveStore } from "./liveStore";

afterEach(() => {
  useLiveStore.setState({
    status: { running: false, pid: 0, mode: "off", startedAt: "", clashApi: false },
    traffic: [],
    totalUp: 0,
    totalDown: 0,
    connections: [],
    latency: { selected: "", rows: [] },
    console: [],
  });
});

describe("liveStore", () => {
  it("subscribes and updates status from a status event", () => {
    const teardown = initLive();
    act(() => handlers.status({ running: true, pid: 7, mode: "vpn", startedAt: "t", clashApi: true }));
    expect(useLiveStore.getState().status.mode).toBe("vpn");
    expect(useLiveStore.getState().status.pid).toBe(7);
    teardown();
  });

  it("accumulates traffic samples with rates and totals", () => {
    const teardown = initLive();
    act(() => handlers.traffic({ up: 100, down: 200, upRate: 10, downRate: 20 }));
    const state = useLiveStore.getState();
    expect(state.totalUp).toBe(100);
    expect(state.totalDown).toBe(200);
    expect(state.traffic).toEqual([{ up: 10, down: 20 }]);
    teardown();
  });

  it("caps the console ring at 500 lines", () => {
    initLive();
    const lines = Array.from({ length: 600 }, (_unused, index) => ({
      id: index,
      pid: 1,
      app: "x",
      stream: "stdout",
      text: `${index}`,
    }));
    act(() => handlers.console(lines));
    expect(useLiveStore.getState().console).toHaveLength(500);
  });

  it("unsubscribes all handlers on teardown", () => {
    const teardown = initLive();
    teardown();
    expect(handlers.status).toBeUndefined();
    expect(handlers.traffic).toBeUndefined();
  });
});
