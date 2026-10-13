import { CheckIcon, ErrorIcon, WarningIcon } from '../icons';
import type { ExplorerState } from '../state/store';
import { Button } from './ui';

/** Shown instead of the workspace when the engine refuses this page. */
export function NoToken() {
  return (
    <section className="m-4 max-w-2xl rounded border border-danger p-4" role="alert">
      <h2 className="flex items-center gap-2 font-display text-lg font-semibold text-danger">
        <ErrorIcon />
        No valid token
      </h2>
      <p className="mt-2">
        This page has no access token, or the daemon rejected it. The token is kept in memory only, so it is gone after a reload, and a restarted
        daemon issues a new one.
      </p>
      <p className="mt-2">
        Open the URL printed by <code className="font-mono text-fg-strong">sunspec-explorerd url</code> on the machine that runs the daemon.
      </p>
    </section>
  );
}

interface LinkBannerProps {
  link: ExplorerState['link'];
  restored: ExplorerState['restored'];
  /** A recording is running: it does not depend on this window. */
  recording: boolean;
  onDismiss: () => void;
}

/** The state of the link to the daemon while it is lost, and how it came back. */
export function LinkBanner({ link, restored, recording, onDismiss }: LinkBannerProps) {
  if (link === 'down') {
    return (
      <div className="flex shrink-0 items-center gap-2 border-b border-danger px-4 py-2 text-danger" role="alert">
        <ErrorIcon />
        <span>
          <strong className="font-semibold">Connection to the daemon lost — retrying.</strong> The values below are the last ones received; the
          daemon keeps polling{recording && ' and recording'}.
        </span>
      </div>
    );
  }
  if (link !== 'up' || restored === null) {
    return null;
  }
  const gap = restored === 'gap';
  return (
    <div className={`flex shrink-0 items-center gap-2 border-b px-4 py-2 ${gap ? 'border-wire text-wire' : 'border-accent text-accent'}`} role="status">
      {gap ? <WarningIcon /> : <CheckIcon />}
      <span className="flex-1">
        {gap
          ? 'Connection to the daemon restored, but some events were missed while disconnected. The current state was read again.'
          : 'Connection to the daemon restored. Events sent while disconnected were replayed.'}
      </span>
      <Button onClick={onDismiss}>Dismiss</Button>
    </div>
  );
}
