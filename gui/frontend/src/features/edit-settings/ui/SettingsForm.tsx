import { type PropsWithChildren, useCallback, useEffect, useState } from "react";

import { api, type TSettings } from "@/shared/api/singctl";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { EmptyState } from "@/shared/ui/empty-state";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";
import { Toggle } from "@/shared/ui/toggle";

export interface SettingsFormProps {
  onStopDaemon: () => void;
}

interface FieldRowProps extends PropsWithChildren {
  label: string;
}

function FieldRow({ label, children }: FieldRowProps) {
  return (
    <Stack direction="row" justify="between" align="center" className="border-b border-border py-3 last:border-0">
      <Text tone="dim">{label}</Text>
      <div className="w-[280px]">{children}</div>
    </Stack>
  );
}

// SettingsForm fetches the daemon tunables, edits them locally, and applies them
// back (which reloads the core). It also exposes the stop-daemon action.
export function SettingsForm({ onStopDaemon }: SettingsFormProps) {
  const [settings, setSettings] = useState<TSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const apply = useCallback(async () => {
    if (!settings) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      await api.applySettings(settings);
      setMessage("Applied. The core reloaded with the new settings.");
    } catch (caught) {
      setMessage(String(caught));
    } finally {
      setBusy(false);
    }
  }, [settings]);

  useEffect(() => {
    api.getSettings().then(setSettings).catch((caught) => setMessage(String(caught)));
  }, []);

  if (!settings) {
    return (
      <Card>
        <EmptyState>{message || "Loading settings… (is the daemon running?)"}</EmptyState>
      </Card>
    );
  }

  const update = <Key extends keyof TSettings>(key: Key, value: TSettings[Key]) =>
    setSettings({ ...settings, [key]: value });

  return (
    <Stack gap="md">
      {message && <Text tone="dim" size="sm">{message}</Text>}
      <Card>
        <FieldRow label="SOCKS port">
          <Input
            aria-label="socks-port"
            type="number"
            value={settings.SocksPort}
            onChange={(event) => update("SocksPort", Number.parseInt(event.target.value || "0", 10))}
          />
        </FieldRow>
        <FieldRow label="Clash API">
          <Toggle aria-label="clash-enabled" checked={settings.ClashEnabled} onChange={(value) => update("ClashEnabled", value)} />
        </FieldRow>
        <FieldRow label="Clash API address">
          <Input
            aria-label="clash-addr"
            value={settings.ClashAddr}
            disabled={!settings.ClashEnabled}
            onChange={(event) => update("ClashAddr", event.target.value)}
          />
        </FieldRow>
        <FieldRow label="URLTest URL">
          <Input aria-label="urltest-url" value={settings.URLTestURL} onChange={(event) => update("URLTestURL", event.target.value)} />
        </FieldRow>
        <FieldRow label="URLTest interval">
          <Input
            aria-label="urltest-interval"
            placeholder="e.g. 3m"
            value={settings.URLTestInterval}
            onChange={(event) => update("URLTestInterval", event.target.value)}
          />
        </FieldRow>
        <FieldRow label="URLTest tolerance (ms)">
          <Input
            aria-label="urltest-tolerance"
            type="number"
            value={settings.URLTestTolerance}
            onChange={(event) => update("URLTestTolerance", Number.parseInt(event.target.value || "0", 10))}
          />
        </FieldRow>
        <FieldRow label="Save profile to disk">
          <Toggle aria-label="save-profile" checked={settings.SaveProfile} onChange={(value) => update("SaveProfile", value)} />
        </FieldRow>
      </Card>
      <Stack direction="row" justify="between">
        <Button variant="primary" disabled={busy} onClick={apply}>Apply</Button>
        <Button variant="danger" disabled={busy} onClick={onStopDaemon}>Stop daemon</Button>
      </Stack>
    </Stack>
  );
}
