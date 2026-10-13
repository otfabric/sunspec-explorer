import { describe, expect, it, vi } from 'vitest';
import { readToken, takeToken } from './token';

describe('readToken', () => {
  it('reads the token from the fragment', () => {
    expect(readToken('#token=abc123')).toBe('abc123');
    expect(readToken('#x=1&token=a%2Bb')).toBe('a+b');
  });

  it('returns null without a token', () => {
    expect(readToken('')).toBeNull();
    expect(readToken('#token=')).toBeNull();
    expect(readToken('#section')).toBeNull();
  });
});

describe('takeToken', () => {
  const fakeWindow = (hash: string) => ({
    location: { hash, pathname: '/', search: '?mock' } as Location,
    history: { state: { kept: true }, replaceState: vi.fn() } as unknown as History,
  });

  it('strips the fragment from the address bar and keeps path and query', () => {
    const win = fakeWindow('#token=secret');
    expect(takeToken(win)).toBe('secret');
    expect(win.history.replaceState).toHaveBeenCalledWith({ kept: true }, '', '/?mock');
  });

  it('leaves the address alone when there is no token', () => {
    const win = fakeWindow('#details');
    expect(takeToken(win)).toBeNull();
    expect(win.history.replaceState).not.toHaveBeenCalled();
  });
});
