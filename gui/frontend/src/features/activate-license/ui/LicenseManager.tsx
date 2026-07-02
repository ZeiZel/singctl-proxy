import { type ChangeEvent, useCallback, useState } from "react";

import { LicenseStatusBadge, useLicense } from "@/entities/license";
import { api } from "@/shared/api/singctl";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Stack } from "@/shared/ui/stack";
import { Heading, Text } from "@/shared/ui/text";
import { Textarea } from "@/shared/ui/textarea";

// A pragmatic non-empty-and-has-an-@ check: this only gates the button, the
// server is the authority on whether the address is usable.
function isValidEmail(value: string): boolean {
  const trimmed = value.trim();
  return trimmed.length > 0 && /\S+@\S+\.\S+/.test(trimmed);
}

function formatExpiry(expiresAt: number): string {
  if (expiresAt === 0) {
    return "perpetual";
  }
  return new Date(expiresAt * 1000).toISOString().slice(0, 10);
}

// LicenseManager shows the license status and lets the user paste/import a token
// or remove the current one. The daemon applies a new license on its next
// (auto-)restart, so no manual restart is required.
export function LicenseManager() {
  const { info, refresh } = useLicense();
  const [token, setToken] = useState("");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const canActivate = token.trim().length > 0 && isValidEmail(email);

  const handleActivate = useCallback(async () => {
    if (!token.trim() || !isValidEmail(email)) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      await api.activateLicense(token.trim(), email.trim());
      setToken("");
      setMessage("License activated. The service will apply it automatically.");
      refresh();
    } catch (caught) {
      setMessage(String(caught));
    } finally {
      setBusy(false);
    }
  }, [token, email, refresh]);

  const handleRemove = useCallback(async () => {
    if (!window.confirm("Remove the stored license?")) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      await api.removeLicense();
      setMessage("License removed.");
      refresh();
    } catch (caught) {
      setMessage(String(caught));
    } finally {
      setBusy(false);
    }
  }, [refresh]);

  const handleFile = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) {
      return;
    }
    const reader = new FileReader();
    reader.onload = () => setToken(String(reader.result).trim());
    reader.readAsText(file);
  }, []);

  return (
    <Stack gap="md">
      {message && <Text tone="dim" size="sm">{message}</Text>}

      <Card>
        <Stack direction="row" justify="between" align="center" className="mb-3">
          <Heading level={3}>Status</Heading>
          <LicenseStatusBadge info={info} />
        </Stack>
        {!info.enforced ? (
          <Text tone="dim">Development build — license enforcement is disabled.</Text>
        ) : info.valid ? (
          <Stack gap="xs">
            <Text>
              Licensed to <Text weight="semibold">{info.subject}</Text>
            </Text>
            <Text tone="dim" size="sm">
              Expires: {formatExpiry(info.expiresAt)}
              {info.daysLeft >= 0 ? ` (${info.daysLeft} days left)` : ""}
            </Text>
            {info.features?.length ? (
              <Text tone="dim" size="sm">Features: {info.features.join(", ")}</Text>
            ) : null}
          </Stack>
        ) : (
          <Text tone="danger">Not licensed: {info.reason}</Text>
        )}
      </Card>

      {info.enforced && (
        <Card>
          <Heading level={3} className="mb-3">Activate</Heading>
          <Stack gap="sm">
            <Input
              aria-label="license-email"
              type="email"
              placeholder="you@example.com"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
            <Textarea
              aria-label="license-token"
              rows={4}
              placeholder="Paste your license token (SINGCTL-LIC.v1.…)"
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
            <Stack direction="row" gap="sm" align="center" wrap>
              <Button variant="primary" disabled={busy || !canActivate} onClick={handleActivate}>
                Activate
              </Button>
              <label className="cursor-pointer text-sm text-text-dim hover:text-text">
                Import file…
                <input type="file" className="hidden" onChange={handleFile} />
              </label>
              <div className="flex-1" />
              {info.valid && (
                <Button variant="danger" disabled={busy} onClick={handleRemove}>
                  Remove license
                </Button>
              )}
            </Stack>
          </Stack>
        </Card>
      )}
    </Stack>
  );
}
