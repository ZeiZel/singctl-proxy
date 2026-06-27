import { useCallback, useEffect, useState } from "react";

import { api, type TProcInfo } from "@/shared/api/singctl";

const REFRESH_INTERVAL_MS = 4000;

export interface IProxyTargets {
  processes: TProcInfo[];
  routed: number[];
  refresh: () => void;
}

// useProxyTargets polls the daemon for the process list and the routed PIDs (no
// push-event exists for these), exposing a manual refresh for after an action.
export function useProxyTargets(running: boolean): IProxyTargets {
  const [processes, setProcesses] = useState<TProcInfo[]>([]);
  const [routed, setRouted] = useState<number[]>([]);

  const refresh = useCallback(async () => {
    try {
      const [processList, routedPids] = await Promise.all([api.listProcesses(), api.listRouted()]);
      setProcesses(processList ?? []);
      setRouted(routedPids ?? []);
    } catch {
      // The daemon may be offline; the next tick retries.
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, REFRESH_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [refresh, running]);

  return { processes, routed, refresh };
}
