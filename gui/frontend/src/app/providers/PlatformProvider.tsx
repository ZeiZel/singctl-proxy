import { type PropsWithChildren, useEffect } from "react";

import { getPlatform } from "@/shared/api/singctl";

// PlatformProvider records the host OS on <html data-platform> so the stylesheet
// can enable macOS-only vibrancy (transparent body) and fall back to a solid
// background elsewhere.
export function PlatformProvider({ children }: PropsWithChildren) {
  useEffect(() => {
    getPlatform().then((platform) => {
      if (platform) {
        document.documentElement.dataset.platform = platform;
      }
    });
  }, []);

  return <>{children}</>;
}
