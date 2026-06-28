import { type TextareaHTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {}

// Textarea is the multi-line text field primitive (e.g. pasting a license token).
export function Textarea({ className, ...rest }: TextareaProps) {
  return (
    <textarea
      className={cn(
        "w-full select-text rounded-[9px] border border-border bg-bg-soft px-3 py-2.5 font-mono text-xs text-text outline-none focus:border-accent",
        className
      )}
      {...rest}
    />
  );
}
