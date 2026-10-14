export type Theme = 'dark' | 'light';

/**
 * How this window reaches the engine; shown as text in the mode badge.
 * 'local' is the desktop application with its in-process engine, 'browser'
 * the web UI served by a daemon. There is no remote mode in the desktop app.
 */
export type Mode = { kind: 'local' } | { kind: 'browser' };

export type TlsFileKind = 'cert' | 'key' | 'ca';

/** The TLS identity the engine presents to tcp+tls:// targets, as files on its machine. */
export interface TLSSettings {
  cert_file: string;
  key_file: string;
  ca_file: string;
  /** All three files are set and loaded: the engine opens TLS targets. */
  active: boolean;
  /** Why the identity is not active; empty while files are still missing. */
  error: string;
  /** The identity came from the command line and cannot be changed here. */
  from_flags: boolean;
}

/**
 * Lets the user pick the files of the TLS identity with native file dialogs.
 * The window only ever sees the paths those dialogs returned, never contents.
 */
export interface TlsIdentity {
  settings(): Promise<TLSSettings>;
  /** Opens the native Open dialog; cancelling resolves to the unchanged settings. */
  choose(kind: TlsFileKind): Promise<TLSSettings>;
  clear(): Promise<TLSSettings>;
}

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
  /** Present where the user of this window also runs the engine: the desktop application. */
  readonly tls?: TlsIdentity;
}
