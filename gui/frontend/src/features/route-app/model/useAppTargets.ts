import { useCallback, useEffect, useState } from "react";

import { api, type TApplication } from "@/shared/api/singctl";

const REFRESH_INTERVAL_MS = 4000;

export interface IAppTargets {
  applications: TApplication[];
  routed: string[]; // bundle IDs currently routed through the proxy
  refresh: () => void;
}

// useAppTargets polls the daemon for the whole-app list and the routed bundle
// IDs (no push-event exists for these), exposing a manual refresh for after an
// action. Mirrors useProxyTargets, one level up (bundle ID instead of PID).
export function useAppTargets(running: boolean): IAppTargets {
  const [applications, setApplications] = useState<TApplication[]>([]);
  const [routed, setRouted] = useState<string[]>([]);

  const refresh = useCallback(async () => {
    try {
      const [applicationList, routedIds] = await Promise.all([api.listApplications(), api.listRoutedApps()]);
      setApplications(applicationList ?? []);
      setRouted(routedIds ?? []);
    } catch {
      // The daemon may be offline; the next tick retries.
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, REFRESH_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [refresh, running]);

  return { applications, routed, refresh };
}
