import { SettingsForm } from "@/features/edit-settings";
import { stopDaemon } from "@/features/stop-daemon";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";

// SettingsPage hosts the daemon settings form.
export function SettingsPage() {
  return (
    <Stack gap="md">
      <Heading level={1}>Settings</Heading>
      <SettingsForm onStopDaemon={() => stopDaemon()} />
    </Stack>
  );
}
