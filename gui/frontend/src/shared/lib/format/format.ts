const BYTE_UNITS = ["KB", "MB", "GB", "TB"] as const;

// formatBytes renders a byte count as a human-readable string.
export function formatBytes(count: number): string {
  if (count < 1024) {
    return `${count} B`;
  }
  let value = count / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < BYTE_UNITS.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${BYTE_UNITS[unitIndex]}`;
}

// formatRate renders a per-second byte rate.
export function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`;
}
