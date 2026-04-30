import { Toaster } from "sonner";

import { AppShell } from "@/app/app-shell";

export default function App() {
  return (
    <>
      <Toaster position="top-center" richColors offset={48} />
      <AppShell />
    </>
  );
}
