import { useCallback, useState } from "react";

import { api } from "@/shared/api/singctl";

export interface IRouteProcess {
  busy: boolean;
  error: string;
  route: (pid: number) => void;
  unroute: (pid: number) => void;
  kill: (pid: number) => void;
  restart: (pid: number) => void;
}

// useRouteProcess wraps the daemon per-process actions with shared busy/error
// state and an onChanged callback so callers can refresh their lists.
export function useRouteProcess(onChanged?: () => void): IRouteProcess {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const run = useCallback(
    async (action: () => Promise<unknown>) => {
      setBusy(true);
      setError("");
      try {
        await action();
        onChanged?.();
      } catch (caught) {
        setError(String(caught));
      } finally {
        setBusy(false);
      }
    },
    [onChanged]
  );

  const route = useCallback((pid: number) => run(() => api.routePID(pid)), [run]);
  const unroute = useCallback((pid: number) => run(() => api.unroutePID(pid)), [run]);
  const kill = useCallback((pid: number) => run(() => api.killPID(pid)), [run]);
  const restart = useCallback((pid: number) => run(() => api.restartPID(pid)), [run]);

  return { busy, error, route, unroute, kill, restart };
}
