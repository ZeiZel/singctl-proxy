import { cn } from "@/shared/lib/cn";

export interface ToggleProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  "aria-label"?: string;
}

// Toggle is an accessible on/off switch (a styled checkbox-role button).
export function Toggle({ checked, onChange, disabled, "aria-label": ariaLabel }: ToggleProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={ariaLabel}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative h-6 w-[42px] rounded-full border border-border transition-colors disabled:opacity-50",
        checked ? "bg-accent" : "bg-panel-raised"
      )}
    >
      <span
        className={cn(
          "absolute top-0.5 h-[18px] w-[18px] rounded-full bg-white transition-all",
          checked ? "left-5" : "left-0.5"
        )}
      />
    </button>
  );
}
