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
        "flex flex-col gap-5 border-b border-[#f2f3f5] pb-5 lg:flex-row lg:items-end lg:justify-between",
        className,
      )}
    >
      <div className="flex flex-col gap-1.5">
        <div className="font-mid text-[11px] font-medium tracking-[0.12em] text-[#8e8e93] uppercase">
          {eyebrow}
        </div>
        <h1 className="font-display text-[2rem] leading-[1.15] font-semibold text-foreground sm:text-[2.35rem]">
          {title}
        </h1>
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </section>
  );
}
