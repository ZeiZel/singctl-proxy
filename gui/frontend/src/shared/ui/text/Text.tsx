import { type HTMLAttributes } from "react";

import { cn, cva, type VariantProps } from "@/shared/lib/cn";

const textVariants = cva("", {
  variants: {
    tone: {
      default: "text-text",
      dim: "text-text-dim",
      faint: "text-text-faint",
      accent: "text-accent",
      ok: "text-ok",
      danger: "text-danger",
      warn: "text-warn",
    },
    size: { xs: "text-xs", sm: "text-sm", md: "text-[14px]", lg: "text-lg", xl: "text-2xl" },
    weight: { normal: "font-normal", medium: "font-medium", semibold: "font-semibold", bold: "font-bold" },
    mono: { true: "font-mono", false: "" },
  },
  defaultVariants: { tone: "default", size: "md", weight: "normal", mono: false },
});

export interface TextProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof textVariants> {}

// Text is the inline/paragraph typography primitive (tone, size, weight, mono).
export function Text({ className, tone, size, weight, mono, ...rest }: TextProps) {
  return <span className={cn(textVariants({ tone, size, weight, mono }), className)} {...rest} />;
}
