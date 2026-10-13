import { useCallback, useLayoutEffect, useRef, useState } from 'react';

const OVERSCAN = 12;

/**
 * Windowing for a list of equal-height rows inside a scrolling element: only
 * the rows near the viewport are rendered. `headerHeight` is the sticky header
 * that covers the top of the viewport.
 */
export function useWindow(count: number, rowHeight: number, headerHeight: number) {
  const viewport = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ top: 0, height: 0 });

  useLayoutEffect(() => {
    const element = viewport.current;
    if (!element) {
      return;
    }
    const measure = () =>
      setView((current) =>
        current.top === element.scrollTop && current.height === element.clientHeight
          ? current
          : { top: element.scrollTop, height: element.clientHeight },
      );
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    element.addEventListener('scroll', measure, { passive: true });
    return () => {
      observer.disconnect();
      element.removeEventListener('scroll', measure);
    };
  }, []);

  const start = Math.min(count, Math.max(0, Math.floor(view.top / rowHeight) - OVERSCAN));
  const end = Math.min(count, Math.ceil((view.top + view.height) / rowHeight) + OVERSCAN);

  /** Scrolls just far enough to bring a row out from under the header or the fold. */
  const reveal = useCallback(
    (index: number) => {
      const element = viewport.current;
      if (!element) {
        return;
      }
      const top = index * rowHeight;
      const visible = element.clientHeight - headerHeight;
      if (top < element.scrollTop) {
        element.scrollTop = top;
      } else if (top + rowHeight > element.scrollTop + visible) {
        element.scrollTop = top + rowHeight - visible;
      }
    },
    [rowHeight, headerHeight],
  );

  const pageSize = Math.max(1, Math.floor((view.height - headerHeight) / rowHeight) - 1);
  return { viewport, start, end, reveal, pageSize };
}
