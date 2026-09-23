# Secret

| Lingkungan   | Tempat                                     | Cara                                                    |
| ------------ | ------------------------------------------ | ------------------------------------------------------- |
| Compose lite | `.env` (tidak di-commit)                   | `make env` membuat password acak                        |
| k3d + Tilt   | Secret Kubernetes di namespace `siaga`     | `scripts/dev-secrets.sh`, dijalankan otomatis oleh Tilt |
| Produksi     | `deploy/k8s/prod/*.enc.yaml` (terenkripsi) | SOPS + age                                              |

## Menyiapkan SOPS untuk produksi

```bash
mkdir -p ~/.config/sops/age
age-keygen -o ~/.config/sops/age/keys.txt   # private key: simpan juga di password manager
grep 'public key' ~/.config/sops/age/keys.txt
```

Ganti nilai `age:` di `.sops.yaml` dengan public key tadi. Buat secret lalu enkripsi:

```bash
sops --encrypt --in-place deploy/k8s/prod/db-roles.enc.yaml
```

Private key tidak pernah di-commit. `gitleaks` di pre-commit dan CI menolak commit yang berisi pola key.
