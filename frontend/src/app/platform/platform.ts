export type Theme = 'dark' | 'light';

/**
 * How this window reaches the engine; shown as text in the mode badge.
 * 'local' is the desktop application with its in-process engine, 'browser'
 * the web UI served by a daemon. There is no remote mode in the desktop app.
 */
export type Mode = { kind: 'local' } | { kind: 'browser' };

/** What the shared code needs from its host besides the Service. */
export interface Platform {
  readonly mode: Mode;
  /** Saves text to a file the user chooses. Resolves to false when cancelled. */
  saveFile(name: string, mime: string, text: string): Promise<boolean>;
  /** Like saveFile, for data that is already a Blob (e.g. a downloaded recording). */
  saveBlob(name: string, blob: Blob): Promise<boolean>;
  openExternal(url: string): void;
  persistTheme(theme: Theme): void;
  /** The theme the user chose earlier, or null on first run. */
  loadTheme(): Theme | null;
  copyText(text: string): Promise<void>;
  /** Loads the page again, e.g. after the engine was updated under it. */
  reload(): void;
}
