import { LicenseManager } from "@/features/activate-license";
import { Stack } from "@/shared/ui/stack";
import { Heading } from "@/shared/ui/text";

// LicensePage hosts the license status + activation flow.
export function LicensePage() {
  return (
    <Stack gap="md">
      <Heading level={1}>License</Heading>
      <LicenseManager />
    </Stack>
  );
}
