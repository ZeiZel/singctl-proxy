import { type HTMLAttributes } from "react";

import { cn, cva, type VariantProps } from "@/shared/lib/cn";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold",
  {
    variants: {
      tone: {
        neutral: "bg-panel-raised text-text-dim",
        ok: "bg-ok/15 text-ok",
        danger: "bg-danger/15 text-danger",
        accent: "bg-accent/15 text-accent",
        warn: "bg-warn/15 text-warn",
      },
    },
    defaultVariants: { tone: "neutral" },
  }
);

export interface BadgeProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

// Badge is a small status pill (tone-coloured).
export function Badge({ className, tone, ...rest }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone }), className)} {...rest} />;
}
