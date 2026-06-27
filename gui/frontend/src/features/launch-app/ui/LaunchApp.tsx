import { useCallback, useState } from "react";

import { api } from "@/shared/api/singctl";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";

export interface LaunchAppProps {
  onLaunched?: () => void;
}

// LaunchApp lets the user start a command with its traffic routed through the
// proxy. The command string is split into argv on whitespace.
export function LaunchApp({ onLaunched }: LaunchAppProps) {
  const [command, setCommand] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const handleLaunch = useCallback(async () => {
    const argv = command.trim().split(/\s+/).filter(Boolean);
    if (argv.length === 0) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.launchApp(argv);
      setCommand("");
      onLaunched?.();
    } catch (caught) {
      setError(String(caught));
    } finally {
      setBusy(false);
    }
  }, [command, onLaunched]);

  return (
    <Stack gap="sm">
      <Stack direction="row" gap="sm">
        <Input
          aria-label="launch-command"
          placeholder="e.g. /usr/bin/zen-browser  or  curl https://example.com"
          value={command}
          onChange={(event) => setCommand(event.target.value)}
          onKeyDown={(event) => event.key === "Enter" && handleLaunch()}
        />
        <Button variant="primary" disabled={busy || command.trim().length === 0} onClick={handleLaunch}>
          Launch
        </Button>
      </Stack>
      {error && (
        <Text tone="danger" size="xs">
          {error}
        </Text>
      )}
    </Stack>
  );
}
