# 0019. Pemilihan sel titik sungai dari reanalisis GloFAS

Tanggal: 2026-09-30 · Status: diterima

## Konteks

`make river-snap` (ADR 0011 butir 5) memilih sel GloFAS untuk setiap titik pantau sungai dari debit rata-rata **jendela prakiraan 14 hari** (7 hari lalu sampai 7 hari ke depan). Di dalam radius pencarian, sel alur utama adalah sel dengan debit ≥ 30% debit terbesar; dari sel itu dipilih yang terdekat dengan koordinat perkiraan.

Hasilnya tidak stabil. Dijalankan ulang 2026-09-28 22.16 WIB tanpa mengubah `docs/calibration/titik-sungai-32.csv`, 7 titik pindah sel karena debit jendela 14 hari naik ±2 kali di awal musim hujan:

- `ciliwung-bogor` jatuh ke sel Cisadane (-6,5750, 106,7750, sama dengan `cisadane-bogor`).
- `cimanuk-muara` bergeser 16,5 km ke sel yang debitnya lebih kecil dari Jatibarang.
- `cimanuk-jatibarang` pindah satu sel; `cisadane-hulu`, `cisadane-batas-banten`, `cipunagara-subang`, dan `cipunagara-pamanukan` pindah tanpa tanda apa pun.
- `bekasi-cikeas` dan `ciliwung-depok` tetap berbagi sel -6,4250, 106,8750.

Hasil itu tidak di-commit. Penyebabnya: di jendela pendek, sel kecil yang baru kena hujan lokal bisa melewati ambang 30% sel alur utama, dan urutan debit antarsel ikut hujan minggu itu, bukan luas daerah tangkapan. Pemeriksaan manual di peta juga sia-sia bila pilihan otomatis berubah setiap kali dijalankan.

Kuota Open-Meteo dihitung per lokasi dengan bobot max(1, hari/14 × variabel/10) (HANDOFF, temuan 1e-1). Pencarian 38 titik memakai 816 titik permintaan unik (radius 0,05° = 9 sel, 0,1° = 25 sel). Reanalisis penuh 1984 sampai Juli 2022 (±14.090 hari, bobot ±100,6) untuk semua titik itu ±82 ribu panggilan: delapan hari kuota harian sisa ingest (±6.500), dan seperempat kuota bulanan 300 ribu.

## Keputusan

1. **Sel dipilih dari debit rata-rata periode acuan reanalisis**, `models=consolidated_v4`, bukan jendela prakiraan. Reanalisis tidak berubah, jadi jalan ulang memberi pilihan yang sama. Aturan pemilihan dan tanda di domain tidak berubah.
2. **Periode acuan bawaan 2020-07-01 sampai 2022-06-30** (730 hari, dua tahun hidrologi Juli–Juni, jadi dua musim hujan dan dua kemarau). Rata-rata setahun penuh atau lebih hampir sebanding dengan luas daerah tangkapan hulu sel, yang memang dicari untuk membedakan alur utama dari anak sungai dan sel darat. Bobot ±5,2 per lokasi, total ±4.255 panggilan untuk jalan pertama: muat di sisa kuota harian dan di bawah 5.000 per jam. Periode bisa diganti dengan `-from`/`-to` (tidak boleh sebelum 1997-01-01; awalnya 1984-01-01, dikoreksi ADR 0020 karena reanalisis di Open-Meteo baru berisi sejak 1997), model dengan `-model`.
3. **Cache per titik permintaan** di `.cache/river-snap/glofas-<model>-<dari>-<sampai>.json`: sel yang dijawab, debit rata-rata (4 desimal), dan jumlah hari berisi. Disimpan setelah setiap request (tulis ke file sementara lalu rename), urut tetap. Jalan ulang, misal setelah titik diverifikasi manual (radius 0), tidak memakai kuota, dan hasil jalan pertama sama persis dengan jalan dari cache (rata-rata sudah dibulatkan sebelum dipakai). File cache untuk periode atau model lain ditolak.
4. **Titik uji dulu.** Bila cache masih kosong, request pertama hanya berisi koordinat perkiraan titik pertama. Bila sumber tidak menjawab debit sama sekali (nama model salah atau periode di luar data reanalisis), alat berhenti dengan `ErrEmptyReanalysis` setelah satu panggilan, bukan setelah ribuan.
5. **Jumlah hari dijaga.** Setiap lokasi harus menjawab tepat sejumlah hari periode acuan (`ErrStructure` bila tidak). Sel yang hari berisinya kurang dari 90% periode (sel laut, ujung data) dianggap tanpa debit.
6. **Batas kuota berbobot.** Satu request paling banyak 200 panggilan (38 lokasi pada bobot 5,2), jeda 400 panggilan per menit supaya ingest (paling banyak ±180 per menit) tetap muat di 600 per menit. `-max-calls` (bawaan 4.500) membatasi panggilan baru dalam satu kali jalan; request terakhir dipotong supaya pas, sisanya dilanjutkan dari cache di jalan berikutnya. Titik permintaan yang sama untuk dua titik pantau (misal Cileungsi dan Cikeas) hanya diminta sekali.
7. **Reanalisis penuh hanya untuk sel terpilih**, di langkah ambang persentil banjir berikutnya. (Dilaksanakan ADR 0020: reanalisis berisi 1997-01-01 sampai 2025-05-31, ambang dari 1997–2024, ±73 panggilan per sel.)

## Konsekuensi

- Pemeriksaan manual di peta (`.cache/titik-sungai.geojson`) kini bermakna: titik yang tidak diubah tidak akan pindah sel lagi.
- Laporan `docs/calibration/titik-sungai.md` menyebut model dan periode acuan; kolom debitnya kini debit rata-rata periode acuan, jadi tidak bisa dibandingkan langsung dengan laporan 24 Sep (jendela 14 hari musim kemarau).
- Rata-rata dua tahun bisa berbeda sedikit dari rata-rata 38 tahun. Yang dipakai hanya urutan dan rasio antarsel yang bersebelahan, yang ditentukan luas daerah tangkapan; titik yang tetap meragukan diperiksa manual seperti sebelumnya.
- Cache ada di `.cache/` (tidak di-commit). Di mesin lain jalan pertama memakai kuota lagi, tetapi hasilnya sama karena reanalisis tetap.
- Bobot kuota dihitung dari rumus Open-Meteo yang dibaca di source-nya. Bila rumusnya berubah dan request ditolak 429, cache menyimpan yang sudah diambil; jalankan lagi dengan `-max-calls` lebih kecil.
