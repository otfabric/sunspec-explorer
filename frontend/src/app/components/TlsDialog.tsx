import { useEffect, useState } from 'react';
import { CheckIcon, ErrorIcon, WarningIcon } from '../icons';
import type { TLSSettings, TlsFileKind, TlsIdentity } from '../platform/platform';
import { Dialog } from './Dialog';
import { Button } from './ui';

const FILES: { kind: TlsFileKind; label: string; field: 'cert_file' | 'key_file' | 'ca_file' }[] = [
  { kind: 'cert', label: 'Certificate (PEM)', field: 'cert_file' },
  { kind: 'key', label: 'Private key (PEM)', field: 'key_file' },
  { kind: 'ca', label: 'CA or device certificate (PEM)', field: 'ca_file' },
];

const fileName = (path: string) => path.split(/[\\/]/).pop() ?? path;

function Status({ settings }: { settings: TLSSettings }) {
  if (settings.active) {
    return (
      <p className="flex items-center gap-1.5 text-accent" role="status">
        <CheckIcon size={16} />
        Active: TLS targets can be opened
      </p>
    );
  }
  if (settings.error !== '') {
    return (
      <p className="flex items-center gap-1.5 text-danger" role="status">
        <ErrorIcon size={16} className="shrink-0" />
        {settings.error}
      </p>
    );
  }
  return (
    <p className="flex items-center gap-1.5 text-wire" role="status">
      <WarningIcon size={16} />
      Choose all three files
    </p>
  );
}

interface TlsDialogProps {
  tls: TlsIdentity;
  /** The identity changed: what the engine can do has to be asked again. */
  onChanged: () => void;
  onError: (err: unknown) => void;
  onClose: () => void;
}

/** The files of the TLS identity, chosen with the platform's native file dialogs. */
export function TlsDialog({ tls, onChanged, onError, onClose }: TlsDialogProps) {
  const [settings, setSettings] = useState<TLSSettings | null>(null);

  useEffect(() => {
    tls.settings().then(setSettings).catch(onError);
  }, [tls, onError]);

  function apply(change: Promise<TLSSettings>) {
    change
      .then((next) => {
        setSettings(next);
        onChanged();
      })
      .catch(onError);
  }

  const anySet = settings !== null && FILES.some(({ field }) => settings[field] !== '');
  return (
    <Dialog title="TLS identity" onClose={onClose}>
      <p className="mt-2 text-[13px]">
        Modbus/TCP Security is mutual TLS: this is the identity this application presents, and what it accepts from the device. Private key files stay
        on this computer; the app reads them, nothing is uploaded.
      </p>
      {settings === null ? (
        <p className="mt-4 text-muted">Loading…</p>
      ) : (
        <>
          {settings.from_flags && (
            <p className="mt-3 flex items-start gap-1.5 text-[13px] text-wire">
              <WarningIcon size={16} className="mt-0.5 shrink-0" />
              This identity was given on the command line (--tls-cert, --tls-key, --tls-ca) and cannot be changed here.
            </p>
          )}
          <ul className="mt-4 flex flex-col gap-3">
            {FILES.map(({ kind, label, field }) => (
              <li key={kind} className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                  <p className="ot-label">{label}</p>
                  {settings[field] === '' ? (
                    <p className="mt-1 text-muted">not chosen</p>
                  ) : (
                    <>
                      <p className="mt-1 truncate font-medium text-fg-strong">{fileName(settings[field])}</p>
                      <p className="truncate font-mono text-[12px] text-muted" title={settings[field]}>
                        {settings[field]}
                      </p>
                    </>
                  )}
                </div>
                <Button onClick={() => apply(tls.choose(kind))} disabled={settings.from_flags} aria-label={`Choose ${label}`}>
                  Choose…
                </Button>
              </li>
            ))}
          </ul>
          <div className="mt-4 text-[13px]">
            <Status settings={settings} />
          </div>
        </>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={() => apply(tls.clear())} disabled={!anySet || settings.from_flags}>
          Clear
        </Button>
        <Button variant="primary" onClick={onClose} autoFocus>
          Close
        </Button>
      </div>
    </Dialog>
  );
}
