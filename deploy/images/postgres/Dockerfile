# syntax=docker/dockerfile:1.7
# Image PostgreSQL SIAGA: PostGIS + TimescaleDB + pgvector + h3-pg.
# Dipakai sama persis oleh Docker Compose (lokal) dan CloudNativePG (k3s). Lihat docs/adr/0002.
ARG PG_MAJOR=17
FROM postgres:${PG_MAJOR}-bookworm

ARG PG_MAJOR
# Image resmi postgres sudah membawa repo apt PGDG (postgis, pgvector, h3).
# TimescaleDB diambil dari repo resminya sendiri.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg; \
    curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey \
      | gpg --dearmor -o /usr/share/keyrings/timescaledb.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/debian/ bookworm main" \
      > /etc/apt/sources.list.d/timescaledb.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      "postgresql-${PG_MAJOR}-postgis-3" \
      "postgresql-${PG_MAJOR}-postgis-3-scripts" \
      "postgresql-${PG_MAJOR}-pgvector" \
      "postgresql-${PG_MAJOR}-h3" \
      "timescaledb-2-postgresql-${PG_MAJOR}"; \
    apt-get purge -y --auto-remove curl gnupg; \
    rm -rf /var/lib/apt/lists/*

# Preload wajib untuk TimescaleDB dan statistik query. CloudNativePG menimpa ini
# lewat spec.postgresql.shared_preload_libraries dengan nilai yang sama.
RUN echo "shared_preload_libraries = 'timescaledb,pg_stat_statements'" \
      >> /usr/share/postgresql/postgresql.conf.sample; \
    echo "timescaledb.telemetry_level = off" \
      >> /usr/share/postgresql/postgresql.conf.sample

# Skrip bootstrap dijalankan sekali saat volume data masih kosong (khusus Compose;
# CloudNativePG memakai postInitApplicationSQLRefs dengan file SQL yang sama).
# Build context = root repo: docker build -f deploy/images/postgres/Dockerfile .
COPY infra/db/bootstrap/*.sql /docker-entrypoint-initdb.d/
COPY deploy/images/postgres/initdb/*.sh /docker-entrypoint-initdb.d/

# UID 999 = user postgres di image resmi; CloudNativePG menjalankannya sebagai 26 lewat securityContext.
USER postgres
