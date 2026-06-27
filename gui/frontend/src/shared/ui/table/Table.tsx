import { type HTMLAttributes, type TdHTMLAttributes, type ThHTMLAttributes } from "react";

import { cn } from "@/shared/lib/cn";

export interface TableProps extends HTMLAttributes<HTMLTableElement> {}

// Table + its parts are thin styled wrappers so data tables stay consistent and
// free of repeated utility strings across widgets.
export function Table({ className, ...rest }: TableProps) {
  return <table className={cn("w-full border-collapse text-[13px]", className)} {...rest} />;
}

export function TableHead({ className, ...rest }: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead className={cn(className)} {...rest} />;
}

export function TableBody({ className, ...rest }: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={cn(className)} {...rest} />;
}

export function TableRow({ className, ...rest }: HTMLAttributes<HTMLTableRowElement>) {
  return <tr className={cn("hover:bg-panel-raised", className)} {...rest} />;
}

export function TableHeaderCell({ className, ...rest }: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      className={cn(
        "sticky top-0 z-10 border-b border-border bg-panel px-3 py-2.5 text-left font-semibold text-text-dim",
        className
      )}
      {...rest}
    />
  );
}

export function TableCell({ className, ...rest }: TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn("select-text border-b border-border/50 px-3 py-2", className)} {...rest} />;
}
