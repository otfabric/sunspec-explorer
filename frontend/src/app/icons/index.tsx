// UI icons in the OT Fabric style (BRANDING.md section 5): 24x24 view box,
// currentColor outline, 1.8 stroke, round caps and joins, no fill. Icons are
// decorative: the control or label next to them carries the name.

import type { ReactNode } from 'react';

interface IconProps {
  size?: number;
  className?: string;
}

function icon(paths: ReactNode) {
  return function Icon({ size = 18, className }: IconProps) {
    return (
      <svg
        viewBox="0 0 24 24"
        width={size}
        height={size}
        className={className}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {paths}
      </svg>
    );
  };
}

export const ConnectIcon = icon(<path d="M9 7V3M15 7V3M7 7h10v4a5 5 0 0 1-10 0V7ZM12 16v5" />);
export const DisconnectIcon = icon(<path d="M9 7V4M15 7V4M7 7h10v3M7 10v1a5 5 0 0 0 7.5 4.3M12 16v5M4 4l16 16" />);
export const RefreshIcon = icon(<path d="M20 12a8 8 0 1 1-2.3-5.6M20 4v4.5h-4.5" />);
export const ReadIcon = icon(<path d="M12 4v11M7.5 10.5 12 15l4.5-4.5M5 20h14" />);
export const PlayIcon = icon(<path d="M7 5v14l12-7L7 5Z" />);
export const StopIcon = icon(<rect x="6" y="6" width="12" height="12" rx="1.5" />);
export const RecordIcon = icon(
  <>
    <circle cx="12" cy="12" r="8" />
    <circle cx="12" cy="12" r="3" />
  </>,
);
export const ExportIcon = icon(<path d="M12 15V4M7.5 8.5 12 4l4.5 4.5M5 14v5h14v-5" />);
export const SearchIcon = icon(
  <>
    <circle cx="11" cy="11" r="6" />
    <path d="m20 20-4.5-4.5" />
  </>,
);
export const ChevronIcon = icon(<path d="m9 6 6 6-6 6" />);
export const WarningIcon = icon(<path d="M12 4 3 19.5h18L12 4ZM12 10v4.5M12 17.2v.3" />);
export const ErrorIcon = icon(
  <>
    <circle cx="12" cy="12" r="8.5" />
    <path d="m9 9 6 6M15 9l-6 6" />
  </>,
);
export const CheckIcon = icon(
  <>
    <circle cx="12" cy="12" r="8.5" />
    <path d="m8.5 12.2 2.4 2.4 4.6-5" />
  </>,
);
export const ClockIcon = icon(
  <>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7.5V12l3 2" />
  </>,
);
export const BrowserIcon = icon(
  <>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M3.5 12h17M12 3.5c2.5 2.3 3.7 5.2 3.7 8.5s-1.2 6.2-3.7 8.5c-2.5-2.3-3.7-5.2-3.7-8.5s1.2-6.2 3.7-8.5Z" />
  </>,
);
export const LocalIcon = icon(<path d="M4 5h16v11H4V5ZM9 20h6M12 16v4" />);
export const CopyIcon = icon(
  <>
    <rect x="8.5" y="8.5" width="11" height="11" rx="2" />
    <path d="M15.5 8.5V6.5a2 2 0 0 0-2-2h-7a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h2" />
  </>,
);
export const SunIcon = icon(
  <>
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8" />
  </>,
);
export const MoonIcon = icon(<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z" />);
