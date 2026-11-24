import type { Clock } from "../../shared/domain/clock.js";
import { sourceList, type SourceListDto } from "../domain/source.js";
import type { SourceReader } from "../ports/source-reader.js";

/** Use case status sumber data untuk label "data terlambat" dan halaman status publik. */
export class SourcesService {
  constructor(
    private readonly reader: SourceReader,
    private readonly clock: Clock,
  ) {}

  async list(): Promise<SourceListDto> {
    const rows = await this.reader.list();
    return sourceList(rows, this.clock.now());
  }
}

export const SOURCES_SERVICE = Symbol("SourcesService");
