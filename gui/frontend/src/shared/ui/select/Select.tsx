import { type SelectHTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {}

// Select is the dropdown primitive matching the Input styling.
export function Select({ className, ...rest }: SelectProps) {
  return (
    <select
      className={cn(
        "w-full rounded-[9px] border border-border bg-bg-soft px-3 py-2.5 text-[14px] text-text outline-none focus:border-accent",
        className
      )}
      {...rest}
    />
  );
}
