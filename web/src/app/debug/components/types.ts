export type DebugTab = "chat-trace" | "search-probe" | "skills-reference" | "editable-files";

export type DebugTabItem = {
  id: DebugTab;
  label: string;
};
