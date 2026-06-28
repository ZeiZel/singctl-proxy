import { useCallback, useEffect, useState } from "react";

import { api, type TLicenseInfo } from "@/shared/api/singctl";

const UNKNOWN: TLicenseInfo = {
  enforced: false,
  valid: true,
  subject: "",
  expiresAt: 0,
  daysLeft: -1,
  features: [],
  reason: "",
};

export interface ILicense {
  info: TLicenseInfo;
  loading: boolean;
  refresh: () => void;
}

// useLicense reads the current license state from the bridge. License changes
// rarely, so it fetches on mount and exposes a manual refresh after activation.
export function useLicense(): ILicense {
  const [info, setInfo] = useState<TLicenseInfo>(UNKNOWN);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setInfo(await api.getLicense());
    } catch {
      // Bridge unavailable (e.g. not in Wails) — keep the permissive default.
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return { info, loading, refresh };
}
