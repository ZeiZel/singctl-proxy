import { type HTMLAttributes } from "react";

import { cn, cva, type VariantProps } from "@/shared/lib/cn";

const headingVariants = cva("font-bold text-text", {
  variants: {
    level: { 1: "text-[22px]", 2: "text-lg", 3: "text-[15px]" },
  },
  defaultVariants: { level: 1 },
});

export interface HeadingProps
  extends HTMLAttributes<HTMLHeadingElement>,
    VariantProps<typeof headingVariants> {}

// Heading renders the matching h1/h2/h3 element for the given level.
export function Heading({ className, level, ...rest }: HeadingProps) {
  const Tag = (`h${level ?? 1}` as "h1" | "h2" | "h3");
  return <Tag className={cn(headingVariants({ level }), className)} {...rest} />;
}
