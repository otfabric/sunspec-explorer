import { describe, expect, it } from 'vitest';
import { createSseParser, type SseFrame } from './sse';

function parse(...chunks: string[]): SseFrame[] {
  const frames: SseFrame[] = [];
  const push = createSseParser((frame) => frames.push(frame));
  chunks.forEach(push);
  return frames;
}

describe('createSseParser', () => {
  it('parses id, event and data', () => {
    expect(parse('id: 7\nevent: sample\ndata: {"seq":7}\n\n')).toEqual([{ id: '7', event: 'sample', data: '{"seq":7}' }]);
  });

  it('handles frames split across chunks at any position', () => {
    const stream = 'id: 1\nevent: sample\ndata: {"a":1}\n\nid: 2\nevent: resync\ndata: {}\n\n';
    for (let cut = 1; cut < stream.length; cut++) {
      expect(parse(stream.slice(0, cut), stream.slice(cut)).map((frame) => frame.id)).toEqual(['1', '2']);
    }
  });

  it('accepts CRLF line endings, also when a chunk ends between CR and LF', () => {
    expect(parse('id: 3\r\ndata: x\r', '\n\r\n')).toEqual([{ id: '3', event: undefined, data: 'x' }]);
  });

  it('joins multi-line data and skips comments and frames without data', () => {
    expect(parse(': keep-alive\n\nevent: ping\n\ndata: a\ndata: b\n\n')).toEqual([{ id: undefined, event: undefined, data: 'a\nb' }]);
  });

  it('does not emit an unterminated frame', () => {
    expect(parse('id: 1\ndata: x\n')).toEqual([]);
  });
});
