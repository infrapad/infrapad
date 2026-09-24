// Standalone-host preferences; shared InfraPad components inherit PatternFly tokens from <html>.
export type Theme = "default" | "felt";
export type ColorScheme = "system" | "light" | "dark";
export type ContrastMode = "system" | "default" | "high-contrast" | "glass";

export interface Appearance {
  theme: Theme;
  colorScheme: ColorScheme;
  contrastMode: ContrastMode;
}

const keys = {
  theme: "infrapad_theme",
  colorScheme: "infrapad_color_scheme",
  contrastMode: "infrapad_contrast_mode",
} as const;

function stored<T extends string>(key: string, options: readonly T[], fallback: T): T {
  try {
    const value = window.localStorage.getItem(key);
    return options.find((option) => option === value) ?? fallback;
  } catch {
    return fallback;
  }
}

export const appearance: Appearance = {
  theme: stored(keys.theme, ["default", "felt"], "default"),
  colorScheme: stored(keys.colorScheme, ["system", "light", "dark"], "system"),
  contrastMode: stored(keys.contrastMode, ["system", "default", "high-contrast", "glass"], "system"),
};

const darkPreference = window.matchMedia("(prefers-color-scheme: dark)");
const contrastPreference = window.matchMedia("(prefers-contrast: more)");
const forcedColors = window.matchMedia("(forced-colors: active)");

function applyAppearance() {
  const root = document.documentElement.classList;
  root.toggle("pf-v6-theme-felt", appearance.theme === "felt");
  root.toggle("pf-v6-theme-dark", appearance.colorScheme === "dark" ||
    (appearance.colorScheme === "system" && darkPreference.matches));
  const highContrast = appearance.contrastMode === "high-contrast" ||
    (appearance.contrastMode === "system" && (contrastPreference.matches || forcedColors.matches));
  root.toggle("pf-v6-theme-high-contrast", highContrast);
  root.toggle("pf-v6-theme-glass", appearance.contrastMode === "glass");
}

export function setAppearance<K extends keyof Appearance>(key: K, value: Appearance[K]) {
  appearance[key] = value;
  try {
    window.localStorage.setItem(keys[key], value);
  } catch {
    // Storage can be blocked (e.g. private mode); the current session still works.
  }
  applyAppearance();
}

// Called before React renders, including configuration loading and error pages.
export function initializeAppearance() {
  applyAppearance();
  for (const preference of [darkPreference, contrastPreference, forcedColors]) {
    preference.addEventListener("change", applyAppearance);
  }
}
