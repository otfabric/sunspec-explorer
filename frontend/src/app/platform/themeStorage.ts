import type { Theme } from './platform';

const KEY = 'sunspec-explorer.theme';

// Storage can be unavailable (private mode, locked-down webviews); the theme
// then simply is not remembered.
export function persistTheme(theme: Theme): void {
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    /* not remembered */
  }
}

export function loadTheme(): Theme | null {
  try {
    const value = localStorage.getItem(KEY);
    return value === 'dark' || value === 'light' ? value : null;
  } catch {
    return null;
  }
}
