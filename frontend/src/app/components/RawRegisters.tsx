import { WarningIcon } from '../icons';
import type { ModelResult } from '../service/types';
import { hex16 } from '../state/format';

const PER_ROW = 8;

/** The registers of a model that has no schema, as a hex and decimal grid. */
export function RawRegisters({ model }: { model: ModelResult }) {
  const registers = model.raw_registers ?? [];
  const first = model.start_address + 2; // after the ID and length registers
  const lines = Array.from({ length: Math.ceil(registers.length / PER_ROW) }, (_, i) => registers.slice(i * PER_ROW, (i + 1) * PER_ROW));
  return (
    <section className="min-h-0 flex-1 overflow-auto p-4" aria-label="Raw registers">
      <h3 className="flex items-center gap-2 font-mono text-[13px] font-medium text-wire">
        <WarningIcon size={16} />
        undecoded — no schema
      </h3>
      <p className="mt-1 max-w-2xl text-muted">
        The registry has no schema for model {model.id}, so its {registers.length} data registers are shown as the device sent them: hexadecimal above,
        unsigned decimal below. Addresses are zero-based.
      </p>
      <table className="mt-4 border-collapse font-mono text-[13px]">
        <thead>
          <tr>
            <th scope="col" className="ot-label px-3 py-1.5 text-left">
              Address
            </th>
            {Array.from({ length: PER_ROW }, (_, i) => (
              <th key={i} scope="col" className="ot-label px-3 py-1.5 text-right">
                +{i}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {lines.map((line, i) => (
            <tr key={i} className="border-t border-line">
              <th scope="row" className="px-3 py-1.5 text-left font-normal text-muted">
                {first + i * PER_ROW}
              </th>
              {line.map((value, j) => (
                <td key={j} className="px-3 py-1.5 text-right">
                  <span className="block text-fg-strong">{hex16(value)}</span>
                  <span className="block text-[12px] text-muted">{value}</span>
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
