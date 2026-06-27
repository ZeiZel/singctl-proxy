import { type HTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface BoxProps extends HTMLAttributes<HTMLDivElement> {}

// Box is the neutral block primitive used instead of a bare <div>.
export function Box({ className, ...rest }: BoxProps) {
  return <div className={cn(className)} {...rest} />;
}
