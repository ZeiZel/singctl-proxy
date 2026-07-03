import { useCallback, useState } from "react";

import { api } from "@/shared/api/singctl";

export interface IProxiedApps {
  busy: boolean;
  error: string;
  launch: (path: string) => void;
  setEnabled: (bundleID: string, enabled: boolean) => void;
  remove: (bundleID: string) => void;
}

// useProxiedApps wraps the per-app proxy actions (launch a bundle, toggle it
// enabled/disabled, remove it) with shared busy/error state and an onChanged
// callback so callers can refresh their lists. Mirrors useRouteApp.
export function useProxiedApps(onChanged?: () => void): IProxiedApps {
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

  const launch = useCallback((path: string) => run(() => api.launchAppBundle(path)), [run]);
  const setEnabled = useCallback(
    (bundleID: string, enabled: boolean) => run(() => api.setAppEnabled(bundleID, enabled)),
    [run]
  );
  const remove = useCallback((bundleID: string) => run(() => api.removeApp(bundleID)), [run]);

  return { busy, error, launch, setEnabled, remove };
}
