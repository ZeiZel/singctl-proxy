import { api } from "@/shared/api/singctl";

const CONFIRM_MESSAGE = "Stop the singctl daemon? Proxying will end.";

// stopDaemon confirms with the user, then asks the daemon to shut down. The
// confirm function is injectable so it can be exercised in tests.
export async function stopDaemon(confirm: (message: string) => boolean = window.confirm): Promise<void> {
  if (!confirm(CONFIRM_MESSAGE)) {
    return;
  }
  await api.stopDaemon();
}
