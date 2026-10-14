import { ClockIcon, ErrorIcon, RefreshIcon } from '../icons';
import { SUPPORTED_API_MAJOR } from '../state/engine';
import { Button } from './ui';

const TOKEN_AGAIN = 'A reload forgets the access token: open the URL with the token again.';

/** Shown instead of the workspace when the engine speaks another API than this page. */
export function IncompatiblePanel({ apiVersion, onReload }: { apiVersion: string; onReload: () => void }) {
  return (
    <section className="m-4 max-w-2xl rounded border border-danger p-4" role="alert">
      <h2 className="flex items-center gap-2 font-display text-lg font-semibold text-danger">
        <ErrorIcon />
        This page does not match the daemon
      </h2>
      <p className="mt-2">
        The daemon speaks API {apiVersion}; this page expects {SUPPORTED_API_MAJOR}.x. The page is probably left over from an older or newer daemon.
        Reload to get the page that belongs to the running one.
      </p>
      <p className="mt-2 text-muted">{TOKEN_AGAIN}</p>
      <Button className="mt-4" variant="primary" icon={<RefreshIcon />} onClick={onReload}>
        Reload
      </Button>
    </section>
  );
}

/** The engine was replaced while this page stayed open. */
export function UpdatedBanner({ version, onReload }: { version: string; onReload: () => void }) {
  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-wire px-4 py-2 text-wire" role="status">
      <RefreshIcon />
      <span className="flex-1">
        The daemon was updated to {version}. Reload to get the matching page. {TOKEN_AGAIN}
      </span>
      <Button onClick={onReload}>Reload</Button>
    </div>
  );
}

export function SkewWarning({ skewMs, onDismiss }: { skewMs: number; onDismiss: () => void }) {
  const seconds = Math.round(Math.abs(skewMs) / 1000);
  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-wire px-4 py-2 text-wire" role="status">
      <ClockIcon />
      <span className="flex-1">
        The daemon&apos;s clock is {seconds} s {skewMs > 0 ? 'ahead of' : 'behind'} this computer. Sample times are the daemon&apos;s.
      </span>
      <Button onClick={onDismiss}>Dismiss</Button>
    </div>
  );
}
