import { Controller, Get, Header, Inject } from "@nestjs/common";
import type { components } from "@siaga/api-client";

type Health = components["schemas"]["Health"];

/** Port pemeriksa kesiapan (database). */
export interface ReadinessProbe {
  check(): Promise<void>;
}

export const READINESS_PROBE = Symbol("ReadinessProbe");
export const VERSION = Symbol("Version");

@Controller()
export class HealthController {
  constructor(
    @Inject(READINESS_PROBE) private readonly probe: ReadinessProbe,
    @Inject(VERSION) private readonly version: string,
  ) {}

  @Get("healthz")
  @Header("cache-control", "no-store")
  health(): Health {
    return { status: "ok", version: this.version };
  }

  @Get("readyz")
  @Header("cache-control", "no-store")
  async ready(): Promise<Health> {
    await this.probe.check();
    return { status: "ok", version: this.version };
  }
}
