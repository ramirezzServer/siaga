/** Jam disuntikkan (aturan proyek: tidak ada waktu sistem langsung di domain). */
export interface Clock {
  now(): Date;
}

export const systemClock: Clock = { now: () => new Date() };

/** Jam tetap untuk test dan putar ulang. */
export function fixedClock(at: Date): Clock {
  return { now: () => new Date(at.getTime()) };
}
