import { useCallback, useLayoutEffect, useState } from 'react';
import type { Platform, Theme } from '../platform/platform';

/** The first run follows the system; after that the user's toggle is remembered. */
function initialTheme(platform: Platform): Theme {
  return platform.loadTheme() ?? (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
}

export function useTheme(platform: Platform): [Theme, () => void] {
  const [theme, setTheme] = useState(() => initialTheme(platform));

  // Before paint, so a light-theme user does not see a dark first frame.
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const toggle = useCallback(() => {
    const next = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);
    platform.persistTheme(next);
  }, [theme, platform]);

  return [theme, toggle];
}
