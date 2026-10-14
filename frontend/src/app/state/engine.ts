import type { Timestamp } from '../service/types';

/** The API major version this build of the user interface is written for. */
export const SUPPORTED_API_MAJOR = 1;

/** A clock difference above this is worth telling the user about. */
export const SKEW_LIMIT_MS = 5000;

export function isCompatible(apiVersion: string): boolean {
  return Number(apiVersion.split('.')[0]) === SUPPORTED_API_MAJOR;
}

/**
 * How far the engine's clock is ahead of this computer's (negative: behind),
 * in milliseconds. The engine read its clock somewhere between the moment the
 * request left and the moment the answer arrived; the midpoint is the best
 * guess, off by at most half the round trip.
 */
export function estimateSkewMs(engineTime: Timestamp, sentAtMs: number, receivedAtMs: number): number {
  return Date.parse(engineTime) - (sentAtMs + receivedAtMs) / 2;
}
