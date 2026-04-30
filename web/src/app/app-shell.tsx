import { AnimatedRoutes } from "@/app/animated-routes";
import { TopNav } from "@/components/top-nav";

export function AppShell() {
  return (
    <main className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex min-h-screen max-w-[1440px] flex-col gap-5 px-3 py-3 sm:px-5 lg:px-6">
        <TopNav />
        <AnimatedRoutes />
      </div>
    </main>
  );
}
