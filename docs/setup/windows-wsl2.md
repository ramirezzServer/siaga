# Setup di Windows (WSL2)

SIAGA dikembangkan di dalam WSL2 Ubuntu. Docker Desktop sudah terpasang di laptop; langkah di bawah melengkapi sisanya.

## 1. Pasang WSL2 Ubuntu

Di PowerShell sebagai Administrator:

```powershell
wsl --install -d Ubuntu-24.04
```

Restart bila diminta, lalu buat user Linux saat Ubuntu pertama kali dibuka.

Batasi memori WSL supaya Windows tetap lega. Buat `C:\Users\<nama>\.wslconfig`:

```ini
[wsl2]
memory=10GB
processors=6
```

Lalu `wsl --shutdown` dan buka Ubuntu lagi. Profil lite butuh ±4 GB; profil full ±10 GB.

## 2. Hubungkan Docker Desktop ke WSL

Docker Desktop → Settings → Resources → WSL integration → aktifkan **Ubuntu-24.04** → Apply & restart. Cek dari Ubuntu: `docker version`.

## 3. Pindahkan repo ke dalam WSL

Jangan bekerja langsung di `/mnt/c/...`: akses file lintas sistem lambat, file watcher Tilt tidak jalan, dan izin eksekusi skrip jadi kacau. Clone repo dari folder Windows ke home WSL (clone, bukan `cp`, supaya izin file sesuai isi git):

```bash
mkdir -p ~/code
git clone "/mnt/c/KULIAH/PROJECT CODE/siaga" ~/code/siaga
cd ~/code/siaga
```

Setelah repo ada di GitHub, cukup `git clone` ke `~/code/siaga`. Buka di VS Code dari Ubuntu dengan `code .` (ekstensi WSL).

## 4. Pasang alat

```bash
sudo apt update && sudo apt install -y build-essential make git openssl age curl unzip

# Go (cek versi terbaru di https://go.dev/dl)
curl -fsSL https://go.dev/dl/go1.27.1.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc && source ~/.bashrc

# Node 22 lewat nvm, lalu pnpm lewat corepack
curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
source ~/.bashrc && nvm install 22 && corepack enable

# golangci-lint dan gitleaks
curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.13.2
go install github.com/zricethezav/gitleaks/v8@latest

# Profil full: k3d, kubectl, helm, tilt
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
sudo snap install kubectl --classic || echo "pasang kubectl manual: https://kubernetes.io/docs/tasks/tools/"

# Secret produksi: sops (age sudah dari apt di atas); langkah dengan checksum di docs/setup/secrets.md
```

## 5. Jalankan

```bash
make doctor   # semua alat wajib harus ✓
make deps     # membuat go.sum; commit hasilnya
make hooks
make up
make seed
make check
```

## 6. Masalah yang pernah terjadi

### `make up` gagal setelah Docker Desktop di-update: `docker-credential-desktop.exe` tidak ditemukan

Terjadi 20 Okt 2026 setelah update ke Docker Desktop v4.96.0. `~/.docker/config.json` di WSL berisi `"credsStore": "desktop.exe"`, tetapi helper `docker-credential-desktop.exe` tidak ada di `PATH` WSL, jadi setiap `docker pull`/`docker build` (termasuk build image `siaga-postgres` di `make up`) gagal sebelum mulai:

```text
error getting credentials - err: exec: "docker-credential-desktop.exe": executable file not found in $PATH
```

Perbaikan: buang kunci `credsStore` (image SIAGA semuanya publik, tidak butuh login registry), lalu jalankan lagi.

```bash
cp ~/.docker/config.json ~/.docker/config.json.bak
python3 -c 'import json,sys; c=json.load(open(sys.argv[1])); c.pop("credsStore",None); json.dump(c,open(sys.argv[2],"w"),indent=2)' \
  ~/.docker/config.json.bak ~/.docker/config.json
make up
```

Volume data tidak tersentuh. Bila kelak perlu `docker login` (misal GHCR privat), kredensial disimpan polos di `config.json`; pertimbangkan memulihkan `credsStore` setelah memastikan `docker-credential-desktop.exe` ada di `PATH` (Docker Desktop → Settings → Resources → WSL integration, matikan lalu nyalakan lagi untuk distro ini).

### `go mod download`, `docker pull`, atau ingest timeout setelah WSL dijalankan ulang

DNS tunneling WSL (resolver `10.255.255.254`) tidak jalan di laptop ini. `/etc/wsl.conf` harus berisi `[network] generateResolvConf = false` dan `/etc/resolv.conf` berisi `nameserver 1.1.1.1`, `nameserver 8.8.8.8`, `options use-vc`.

### Integrasi WSL Docker "Catastrophic failure"

Quit Docker Desktop dulu sebelum `wsl --shutdown`; kalau tidak, integrasi WSL gagal saat WSL hidup lagi. Quit Docker Desktop sepenuhnya lalu buka ulang.
