import { useCallback, useState } from "react";

import { api, type TMode } from "@/shared/api/singctl";
import { cn } from "@/shared/lib/cn";
import { Stack } from "@/shared/ui/stack";
import { Text } from "@/shared/ui/text";

export interface ModeSwitchProps {
  mode: string;
}

const MODE_OPTIONS: { value: TMode; label: string; activeClass: string }[] = [
  { value: "off", label: "Off", activeClass: "bg-panel-raised text-text" },
  { value: "proxy", label: "Proxy", activeClass: "bg-accent text-white" },
  { value: "vpn", label: "VPN", activeClass: "bg-ok text-[#06281c]" },
];

// ModeSwitch is the Off/Proxy/VPN segmented control. It drives the daemon via
// api.setMode and disables itself while a switch is in flight.
export function ModeSwitch({ mode }: ModeSwitchProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const handleSelect = useCallback(async (next: TMode) => {
    setBusy(true);
    setError("");
    try {
      await api.setMode(next);
    } catch (caught) {
      setError(String(caught));
    } finally {
      setBusy(false);
    }
  }, []);

  return (
    <Stack gap="sm" data-testid="mode-switch">
      <Stack direction="row" gap="xs" className="w-full rounded-[11px] border border-border bg-bg-soft p-1 sm:w-auto">
        {MODE_OPTIONS.map((option) => (
          <button
            key={option.value}
            type="button"
            disabled={busy}
            onClick={() => handleSelect(option.value)}
            className={cn(
              "flex-1 rounded-lg px-5 py-2 text-[14px] font-semibold text-text-dim transition-colors hover:text-text disabled:opacity-60 sm:flex-none",
              { [option.activeClass]: mode === option.value }
            )}
          >
            {option.label}
          </button>
        ))}
      </Stack>
      {error && (
        <Text tone="danger" size="xs">
          {error}
        </Text>
      )}
    </Stack>
  );
}
