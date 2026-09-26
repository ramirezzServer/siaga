# Secret

| Lingkungan   | Tempat                                                         | Cara                                                                             |
| ------------ | -------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Compose lite | `.env` (tidak di-commit)                                       | `make env` membuat password acak                                                 |
| k3d + Tilt   | Secret Kubernetes di namespace `siaga`, hanya di cluster lokal | `scripts/dev-secrets.sh`, dijalankan otomatis oleh Tilt (resource `dev-secrets`) |
| Produksi     | `deploy/k8s/prod/secrets.enc.yaml` (terenkripsi, di-commit)    | SOPS + age; didekripsi di cluster oleh sops-secrets-operator (ADR 0017)          |

Nama Secret dan key sama di k3d dan produksi:

| Secret                 | Key                                                                                                                         | Dipakai                        |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------- | ------------------------------ |
| `siaga-db-<layanan>`   | `username`, `password` (basic-auth)                                                                                         | CloudNativePG, geo-processor   |
| `siaga-garage`         | `GARAGE_RPC_SECRET`, `GARAGE_ADMIN_TOKEN`, `GARAGE_METRICS_TOKEN`, `GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY` | Garage, ingest, OTel Collector |
| `siaga-ingest`         | `OPENAQ_API_KEY`, `FIRMS_MAP_KEY` (opsional)                                                                                | ingest                         |
| `siaga-otel-collector` | `OTLP_UPSTREAM_ENDPOINT`, `OTLP_UPSTREAM_AUTHORIZATION`                                                                     | OTel Collector                 |

## Alat

`age` dari apt, `sops` dari rilis GitHub (cek checksum):

```bash
sudo apt install -y age
v=3.13.3; cd /tmp
curl -sSfLO "https://github.com/getsops/sops/releases/download/v$v/sops-v$v.linux.amd64"
curl -sSfLO "https://github.com/getsops/sops/releases/download/v$v/sops-v$v.checksums.txt"
grep " sops-v$v.linux.amd64\$" "sops-v$v.checksums.txt" | sha256sum -c -
sudo install -m 755 "sops-v$v.linux.amd64" /usr/local/bin/sops
```

## Membuat secret produksi

```bash
make secrets-prod
```

Skrip ini:

1. membuat kunci age di `~/.config/sops/age/keys.txt` bila belum ada. **Simpan salinan isinya di password manager**: tanpa kunci itu secret produksi tidak bisa dibuka lagi (sampai kunci cluster ditambahkan di fase 2);
2. mengisi public key di `.sops.yaml` (menggantikan placeholder);
3. membuat password dan token acak, mengambil `OPENAQ_API_KEY`/`FIRMS_MAP_KEY` dari `.env`, dan menanyakan kredensial Grafana Cloud untuk Collector produksi. Buat token **baru** khusus produksi (misal `siaga-produksi`), jangan memakai token laptop;
4. mengenkripsi semuanya ke `deploy/k8s/prod/secrets.enc.yaml`. Plaintext tidak pernah ditulis ke disk.

Menjalankan ulang aman: nilai yang sudah ada tidak diubah, hanya yang belum ada yang dilengkapi. Commit `deploy/k8s/prod/secrets.enc.yaml` dan `.sops.yaml`.

## Mengubah secret

```bash
make secrets-grafana   # ganti endpoint, instance ID, dan token Grafana Cloud (rotasi)
make secrets-edit      # buka di $EDITOR lewat sops; disimpan terenkripsi lagi
make secrets-check     # pastikan semua *.enc.yaml terenkripsi (tanpa kunci; juga jalan di CI)
```

Setelah rotasi, cabut token lama di Grafana Cloud Portal (menu Access Policies, token milik policy stack).

## Produksi (fase 2)

Saat bootstrap cluster: buat kunci age khusus cluster, tambahkan public key-nya ke `.sops.yaml` lalu `sops updatekeys deploy/k8s/prod/secrets.enc.yaml`, pasang sops-secrets-operator dengan kunci itu sebagai Secret (`secretsAsFiles` + `SOPS_AGE_KEY_FILE`, lihat README operator), lalu Argo CD menerapkan `SopsSecret` dan operator membuat Secret biasa di namespace `siaga`.

Private key tidak pernah di-commit. `gitleaks` di pre-commit dan CI menolak commit yang berisi pola key, dan `scripts/sops-check.sh` menolak file `*.enc.yaml` yang berisi nilai tidak terenkripsi.
