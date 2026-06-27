import { KeysManager } from "@/features/manage-keys";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";

// KeysPage hosts the VLESS key manager.
export function KeysPage() {
  return (
    <Stack gap="md">
      <Heading level={1}>Keys</Heading>
      <KeysManager />
    </Stack>
  );
}
