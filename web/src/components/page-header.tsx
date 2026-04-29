import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

type PageHeaderProps = {
  eyebrow: string;
  title: string;
  actions?: ReactNode;
  className?: string;
};

export function PageHeader({ eyebrow, title, actions, className }: PageHeaderProps) {
  return (
    <section
      className={cn(
        "flex flex-col gap-4 border-b border-border/80 pb-4 lg:flex-row lg:items-end lg:justify-between",
        className,
      )}
    >
      <div className="flex flex-col gap-1">
        <div className="text-[11px] font-semibold tracking-[0.14em] text-muted-foreground uppercase">
          {eyebrow}
        </div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </section>
  );
}
