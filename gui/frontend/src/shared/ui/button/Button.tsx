import { type ButtonHTMLAttributes } from "react";

import { cn, cva, type VariantProps } from "@/shared/lib/cn";

const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-[9px] border font-semibold transition-colors disabled:opacity-50 disabled:cursor-not-allowed",
  {
    variants: {
      variant: {
        default: "border-border bg-panel-raised text-text hover:border-accent",
        primary: "border-accent bg-accent text-white hover:brightness-110",
        danger: "border-border bg-panel-raised text-danger hover:border-danger",
        ghost: "border-transparent bg-transparent text-text-dim hover:text-text",
      },
      size: { sm: "px-2.5 py-1 text-xs", md: "px-4 py-2 text-[14px]" },
    },
    defaultVariants: { variant: "default", size: "md" },
  }
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

// Button is the action primitive (variant + size); a native <button> underneath.
export function Button({ className, variant, size, type = "button", ...rest }: ButtonProps) {
  return <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...rest} />;
}
