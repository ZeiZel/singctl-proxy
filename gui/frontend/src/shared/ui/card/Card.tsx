import { type HTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface CardProps extends HTMLAttributes<HTMLDivElement> {}

// Card is the panel surface: bordered, rounded, padded container.
export function Card({ className, ...rest }: CardProps) {
  return (
    <div
      data-slot="card"
      className={cn("rounded-card border border-border bg-panel p-[18px]", className)}
      {...rest}
    />
  );
}
