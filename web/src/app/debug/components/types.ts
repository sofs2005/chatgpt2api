export type DebugTab = "chat-trace" | "search-probe" | "skills-reference";

export type DebugTabItem = {
  id: DebugTab;
  label: string;
};
