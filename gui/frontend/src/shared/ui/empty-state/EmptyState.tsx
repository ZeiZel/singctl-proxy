import { type PropsWithChildren } from "react";

import { cn } from "@/shared/lib/cn";

export interface EmptyStateProps extends PropsWithChildren {
  className?: string;
}

// EmptyState is the centered placeholder shown when a list/section has no data.
export function EmptyState({ children, className }: EmptyStateProps) {
  return (
    <div className={cn("px-5 py-14 text-center text-text-faint", className)} role="status">
      {children}
    </div>
  );
}
