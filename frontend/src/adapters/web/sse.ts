export interface SseFrame {
  id?: string;
  event?: string;
  data: string;
}

/**
 * Incremental parser for a text/event-stream body read with fetch (the
 * EventSource API cannot send the Authorization header). Feed it decoded
 * chunks, split anywhere; it calls onFrame for every complete frame that
 * carries data.
 */
export function createSseParser(onFrame: (frame: SseFrame) => void): (chunk: string) => void {
  let buffer = '';
  let frame: { id?: string; event?: string; data: string[] } = { data: [] };

  function line(text: string): void {
    if (text === '') {
      if (frame.data.length > 0) {
        onFrame({ id: frame.id, event: frame.event, data: frame.data.join('\n') });
      }
      frame = { data: [] };
      return;
    }
    if (text.startsWith(':')) {
      return; // comment, used as keep-alive
    }
    const colon = text.indexOf(':');
    const field = colon === -1 ? text : text.slice(0, colon);
    const value = colon === -1 ? '' : text.slice(colon + 1).replace(/^ /, '');
    if (field === 'data') {
      frame.data.push(value);
    } else if (field === 'id') {
      frame.id = value;
    } else if (field === 'event') {
      frame.event = value;
    }
  }

  return (chunk) => {
    buffer += chunk;
    // A chunk may end between the CR and the LF of a line ending: keep a
    // trailing CR in the buffer until the next chunk shows what follows it.
    const complete = /^([\s\S]*(?:\r\n|\n|\r(?!$)))([\s\S]*)$/.exec(buffer);
    if (!complete) {
      return;
    }
    buffer = complete[2] as string;
    const lines = (complete[1] as string).split(/\r\n|\n|\r/);
    lines.pop(); // the empty string after the last line ending
    lines.forEach(line);
  };
}
