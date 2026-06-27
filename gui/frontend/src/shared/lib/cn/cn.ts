import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn is the single class-merging entry point: clsx semantics (arrays, objects,
// conditionals) deduped through tailwind-merge so later utilities win.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
