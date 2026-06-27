import { type HTMLAttributes } from "react";

import { cn, cva, type VariantProps } from "@/shared/lib/cn";

const stackVariants = cva("flex", {
  variants: {
    direction: { row: "flex-row", col: "flex-col" },
    align: { start: "items-start", center: "items-center", end: "items-end", stretch: "items-stretch" },
    justify: {
      start: "justify-start",
      center: "justify-center",
      between: "justify-between",
      end: "justify-end",
    },
    gap: { none: "gap-0", xs: "gap-1", sm: "gap-2", md: "gap-4", lg: "gap-6" },
    wrap: { true: "flex-wrap", false: "flex-nowrap" },
  },
  defaultVariants: { direction: "col", gap: "md", wrap: false },
});

export interface StackProps
  extends HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof stackVariants> {}

// Stack is the flex layout primitive (direction, alignment, gap) used for all
// spacing instead of ad-hoc margin utilities.
export function Stack({ className, direction, align, justify, gap, wrap, ...rest }: StackProps) {
  return (
    <div className={cn(stackVariants({ direction, align, justify, gap, wrap }), className)} {...rest} />
  );
}
