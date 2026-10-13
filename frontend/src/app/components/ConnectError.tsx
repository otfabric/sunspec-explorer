import { ErrorIcon } from '../icons';
import type { ServiceError } from '../service/service';

/** Why a connect failed, with every detection probe the engine made. */
export function ConnectError({ error }: { error: ServiceError }) {
  const attempts = error.attempts ?? [];
  return (
    <section className="m-4 max-w-3xl rounded border border-danger p-4" role="alert">
      <h2 className="flex items-center gap-2 font-display text-lg font-semibold text-danger">
        <ErrorIcon />
        Could not connect
      </h2>
      <p className="mt-2 font-mono text-[13px] text-fg-strong">{error.message}</p>
      <p className="mt-2 text-muted">
        A wrong unit ID is the usual cause. Check the unit ID first, then the register type and the base addresses under Advanced.
      </p>
      {attempts.length > 0 && (
        <table className="mt-4 w-full text-left text-[13px]">
          <caption className="ot-label pb-2 text-left">Probe attempts</caption>
          <thead>
            <tr className="border-b border-line">
              {['Base address', 'Register type', 'Matched', 'Error'].map((heading) => (
                <th key={heading} className="ot-label py-1.5 pr-4" scope="col">
                  {heading}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="font-mono">
            {attempts.map((attempt) => (
              <tr key={`${attempt.reg_type}-${attempt.base_address}`} className="border-b border-line">
                <td className="py-1.5 pr-4">{attempt.base_address}</td>
                <td className="py-1.5 pr-4">{attempt.reg_type}</td>
                <td className="py-1.5 pr-4">{attempt.matched ? 'yes' : 'no'}</td>
                <td className="py-1.5 pr-4">{attempt.error ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
