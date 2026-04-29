export type ColorTheme = "light" | "dark";

export const COLOR_THEME_STORAGE_KEY = "chatgpt2api:color-theme";

type ThemeTransitionOptions = {
  origin?: {
    x: number;
    y: number;
  };
};

type ViewTransitionDocument = Document & {
  startViewTransition?: (callback: () => void) => void;
};

const THEME_TRANSITION_MAX_ELEMENTS = 2500;
const THEME_TRANSITION_MAX_TABLE_ROWS = 80;

export function getPreferredColorTheme(): ColorTheme {
  if (typeof window === "undefined") {
    return "light";
  }

  try {
    const storedTheme = window.localStorage.getItem(COLOR_THEME_STORAGE_KEY);
    if (storedTheme === "light" || storedTheme === "dark") {
      return storedTheme;
    }
  } catch {
    return "light";
  }

  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyColorThemeToRoot(theme: ColorTheme) {
  const root = document.documentElement;
  root.classList.remove("light", "dark");
  root.classList.add(theme);
  root.style.colorScheme = theme;
}

function shouldAnimateThemeTransition() {
  const startViewTransition = (document as ViewTransitionDocument).startViewTransition;
  if (typeof startViewTransition !== "function") {
    return false;
  }

  const root = document.getElementById("root");
  const elementCount = root?.getElementsByTagName("*").length ?? document.getElementsByTagName("*").length;
  const tableRowCount = root?.querySelectorAll('[data-slot="table-row"]').length ?? 0;
  return elementCount <= THEME_TRANSITION_MAX_ELEMENTS && tableRowCount <= THEME_TRANSITION_MAX_TABLE_ROWS;
}

function getThemeTransitionRadius(x: number, y: number) {
  const maxHorizontalDistance = Math.max(x, window.innerWidth - x);
  const maxVerticalDistance = Math.max(y, window.innerHeight - y);
  return Math.hypot(maxHorizontalDistance, maxVerticalDistance);
}

export function applyColorTheme(theme: ColorTheme, options: ThemeTransitionOptions = {}) {
  if (typeof document === "undefined") {
    return;
  }

  const origin = options.origin;
  if (origin && shouldAnimateThemeTransition()) {
    const root = document.documentElement;
    const radius = getThemeTransitionRadius(origin.x, origin.y);
    root.style.setProperty("--theme-transition-x", `${origin.x}px`);
    root.style.setProperty("--theme-transition-y", `${origin.y}px`);
    root.style.setProperty("--theme-transition-radius", `${radius}px`);

    (document as ViewTransitionDocument).startViewTransition?.(() => {
      applyColorThemeToRoot(theme);
    });
    return;
  }

  applyColorThemeToRoot(theme);
}

export function saveColorTheme(theme: ColorTheme) {
  try {
    window.localStorage.setItem(COLOR_THEME_STORAGE_KEY, theme);
  } catch {
    // The theme still applies for the current page even when storage is unavailable.
  }
}
