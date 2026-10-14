import type { Mode, Platform } from '../../app/platform/platform';
import { loadTheme, persistTheme } from '../../app/platform/themeStorage';

export class WebPlatform implements Platform {
  readonly mode: Mode = { kind: 'browser' };
  persistTheme = persistTheme;
  loadTheme = loadTheme;

  saveFile(name: string, mime: string, text: string): Promise<boolean> {
    return this.saveBlob(name, new Blob([text], { type: mime }));
  }

  /** A browser download; the browser decides where the file goes. */
  async saveBlob(name: string, blob: Blob): Promise<boolean> {
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = name;
    link.click();
    // Not at once: some browsers start the download after this task ends.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    return true;
  }

  openExternal(url: string): void {
    window.open(url, '_blank', 'noopener,noreferrer');
  }

  copyText(text: string): Promise<void> {
    return navigator.clipboard.writeText(text);
  }

  reload(): void {
    window.location.reload();
  }
}
