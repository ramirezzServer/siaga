# 0009. Ingest cuaca BMKG: CAP nowcast dan sapuan prakiraan adm4

Tanggal: 2026-09-24 · Status: diterima

## Konteks

Fase 1c menambah dua sumber BMKG yang bentuknya berbeda dari feed gempa. Peringatan dini cuaca (nowcast) terbit sebagai RSS nasional yang menunjuk satu dokumen CAP 1.2 per peringatan, dalam bahasa Indonesia dan Inggris. Prakiraan cuaca diambil per kelurahan/desa (adm4), satu request per kode, untuk ±5.900 kode Jawa Barat. Batas BMKG 60 request/menit berlaku per IP untuk semua endpoint sekaligus, dan dokumen arsitektur mengalokasikan sampai 50 request/menit untuk prakiraan.

Rekaman asli 2026-09-24 (`services/ingest/internal/adapters/bmkg/testdata`) menunjukkan: 11 peringatan se-Indonesia, semuanya `msgType=Alert`, `severity=Moderate`; satu poligon per kecamatan dengan sampai 150 poligon dan 3.072 titik per pesan; ID pesan berupa OID WMO `2.49.0.1.360.0.<YYYY>.<MM>.<DD>.<xx>.<PP>.<urut>` dengan `<PP>` kode provinsi BPS (Aceh 11 … Gorontalo 75); API prakiraan menjawab 404 untuk kode yang tidak dikenal dan mengirim `ETag`.

## Keputusan

1. **Peringatan CAP lewat dua tahap.** Konektor `bmkg-cap` memolling RSS bahasa Indonesia tiap 2 menit (anggaran BMKG biasa), lalu mengambil dokumen CAP untuk entri yang baru atau berubah (digest isi entri RSS). Setiap dokumen memakai anggaran BMKG yang sama lewat `ports.Throttle`. Entri yang hilang dari RSS dilupakan; ID pesan deterministik tetap mencegah ganda di JetStream.
2. **Saring provinsi dari ID CAP, bukan dari isi.** Segmen `<PP>` OID dibaca sebagai kode provinsi, jadi peringatan di luar `INGEST_CAP_PROVINCES` (default `32`) dilewati tanpa request dokumen. ID yang tidak mengikuti tata letak itu tetap diambil, supaya perubahan format BMKG tidak menghilangkan peringatan diam-diam; geo-processor menyaring ulang dengan irisan wilayah (ADR 0010).
3. **Peringatan lebih penting daripada terjemahan.** Dokumen bahasa Indonesia wajib; dokumen bahasa Inggris (`INGEST_CAP_LANGUAGES`, default `en`) digabung bila identifier, `sent`, severity, dan `msgType`-nya sama. Bila gagal diambil, pesan tetap terbit dengan teks Indonesia, lalu terbit ulang sebagai revisi (isi berubah → ID pesan baru) setelah terjemahannya berhasil. Setiap dokumen dicoba paling banyak 5 kali per entri RSS.
4. **Satu event per pesan CAP.** `raw.weather.bmkg` membawa `siaga.raw.v1.WeatherWarning`: CAP 1.2 lengkap (status, msgType, references, severity/urgency/certainty, effective/onset/expires, teks per bahasa, area dengan poligon) dengan waktu UTC. Pesan latihan, uji, draft, `Ack`, dan `Error` tidak diteruskan. URL `web` yang bukan https dibuang, bukan menolak peringatan.
5. **Daftar adm4 disematkan ke binary.** `make adm4-list` menjalankan `import-regions -dry-run -adm4-out` atas data batas wilayah yang sama dengan `ref.region`, lalu menulis `services/ingest/internal/adapters/regionlist/data/adm4_<PP>.txt` (5.957 kode Jawa Barat, dengan header asal data dan jumlah kode). Ingest tidak membaca schema milik geo-processor, dan kode yang disapu sama persis dengan wilayah yang bisa dipetakan.
6. **Sapuan prakiraan sebagai use case sendiri.** `bmkg-prakiraan` menyapu semua kode tiap 6 jam (`INGEST_FORECAST_INTERVAL`), zona fokus Bandung Raya lebih dulu (`INGEST_FORECAST_FOCUS`), dengan conditional request per kode (`ETag`). Hasil per kode: terbit, tidak berubah (isi sama atau 304), 404 (dicatat, bukan galat), ditolak, atau gagal. Kode yang gagal dicoba sekali lagi di akhir sapuan; 10 kegagalan beruntun atau `Retry-After` menjeda sapuan dengan backoff 1–15 menit lalu melanjutkan dari kode berikutnya. Kemajuan tampil di `/status` bagian `sweeps`.
7. **Jalur prioritas rendah di anggaran bersama.** Sapuan punya anggaran sendiri 50/menit (burst 1) dan hanya memakai anggaran BMKG lewat `Bucket.ReserveLow`: izin diberikan hanya bila setelahnya masih tersisa 2 izin langsung. Polling gempa dan CAP (sekitar 4,5/menit) tidak pernah antre di belakang sapuan; batas 59 request per 60 detik tetap berlaku untuk gabungan semua request. Kedua sifat diuji dengan fuzzing (`FuzzPriorityHeadroom`).
8. **Prakiraan terbit ke `raw.forecast.bmkg`, disimpan di fase 1d.** `siaga.raw.v1.RegionForecast` membawa 20 langkah per 3 jam per desa. Hanya prakiraan yang isinya berubah yang terbit. Konsumen (hypertable `ts.weather_forecast`) dibuat bersama hypertable lain di fase 1d; stream `RAW` menyimpan 7 hari, jadi consumer durable baru membaca sejak awal tanpa kehilangan data.

## Konsekuensi

- Satu sapuan penuh sekitar 2 jam dan memakai ±6.000 request; dengan 4 sapuan per hari itu ±24.000 request, di bawah batas harian yang tersirat dari 60/menit.
- Payload prakiraan sekitar 9 KB per desa; hanya payload yang berubah yang diarsipkan (±36 MB per hari sebelum dikompres, jauh lebih kecil setelah gzip).
- Kode yang tidak dikenal BMKG (404) terlihat di `/status` sebagai `not_found_sample`, bahan untuk mencocokkan versi kode Kemendagri BMKG dengan `ref.region`.
- Perubahan tata letak ID CAP BMKG membuat penyaringan provinsi berhenti bekerja tanpa kehilangan data: semua peringatan diambil dan disaring geo-processor.
