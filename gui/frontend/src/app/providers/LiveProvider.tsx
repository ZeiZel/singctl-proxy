import { type PropsWithChildren, useEffect } from "react";

import { initLive } from "@/entities/daemon";

// LiveProvider wires the live store to the daemon push-events for the app's
// lifetime (subscribe on mount, unsubscribe on unmount).
export function LiveProvider({ children }: PropsWithChildren) {
  useEffect(() => {
    const teardown = initLive();
    return teardown;
  }, []);

  return <>{children}</>;
}
