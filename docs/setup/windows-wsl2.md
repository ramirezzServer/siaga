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
