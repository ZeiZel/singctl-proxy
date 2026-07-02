import { useCallback, useState } from "react";

import { api } from "@/shared/api/singctl";

export interface IRouteApp {
  busy: boolean;
  error: string;
  route: (bundleID: string) => void;
  unroute: (bundleID: string) => void;
}

// useRouteApp wraps the daemon whole-application actions (route/unroute by
// bundle ID) with shared busy/error state and an onChanged callback so callers
// can refresh their lists. Mirrors useRouteProcess, one level up.
export function useRouteApp(onChanged?: () => void): IRouteApp {
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

  const route = useCallback((bundleID: string) => run(() => api.routeApp(bundleID)), [run]);
  const unroute = useCallback((bundleID: string) => run(() => api.unrouteApp(bundleID)), [run]);

  return { busy, error, route, unroute };
}
