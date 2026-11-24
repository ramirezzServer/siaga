import type { SourceRecord } from "../domain/source.js";

/** Port baca status konektor (ts.source_status milik geo-processor, hanya SELECT). */
export interface SourceReader {
  list(): Promise<SourceRecord[]>;
}
