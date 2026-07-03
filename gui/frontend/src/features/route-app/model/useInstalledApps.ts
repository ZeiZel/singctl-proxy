import { useCallback, useEffect, useState } from "react";

import { api, type TInstalledApp } from "@/shared/api/singctl";

export interface IInstalledApps {
  apps: TInstalledApp[];
  loading: boolean;
  error: string;
  refresh: () => void;
}

// useInstalledApps loads the on-disk app bundle enumeration once on mount
// (it can be slow-ish over hundreds of apps) and exposes a manual refresh,
// rather than polling like the running/proxied lists.
export function useInstalledApps(): IInstalledApps {
  const [apps, setApps] = useState<TInstalledApp[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const list = await api.listInstalledApps();
      setApps(list ?? []);
    } catch (caught) {
      setError(String(caught));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return { apps, loading, error, refresh };
}
