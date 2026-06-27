import { type InputHTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {}

// Input is the text field primitive (selectable text, focus ring).
export function Input({ className, type = "text", ...rest }: InputProps) {
  return (
    <input
      type={type}
      className={cn(
        "w-full select-text rounded-[9px] border border-border bg-bg-soft px-3 py-2.5 text-[14px] text-text outline-none focus:border-accent",
        className
      )}
      {...rest}
    />
  );
}
