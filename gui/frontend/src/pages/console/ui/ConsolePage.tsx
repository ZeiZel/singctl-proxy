import { useConsole } from "@/entities/daemon";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";
import { ConsoleViewer } from "@/widgets/console-viewer";

// ConsolePage streams captured per-app stdout/stderr.
export function ConsolePage() {
  const lines = useConsole();
  return (
    <Stack gap="md">
      <Heading level={1}>Console</Heading>
      <ConsoleViewer lines={lines} />
    </Stack>
  );
}
