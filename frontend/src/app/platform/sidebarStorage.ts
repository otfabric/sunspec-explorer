const KEY = 'sunspec-explorer.sidebar';

// A per-viewer convenience: without storage the sidebar simply starts
// expanded every time.
export function persistSidebarCollapsed(collapsed: boolean): void {
  try {
    localStorage.setItem(KEY, collapsed ? 'collapsed' : 'expanded');
  } catch {
    /* not remembered */
  }
}

export function loadSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(KEY) === 'collapsed';
  } catch {
    return false;
  }
}
