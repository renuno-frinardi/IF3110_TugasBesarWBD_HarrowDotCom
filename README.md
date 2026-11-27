# HarrowDotCom

**Kode Kelompok: K02-11**

Tugas besar IF3110 Pengembangan Aplikasi Web.

## Anggota Kelompok

| NIM | Nama Lengkap |
| --- | --- |
| 13524005 | Geraldo Artemius |
| 13524080 | Renuno Yuqa Frinardi |
| 13524104 | Valentino Daniel Kusumo |

## Struktur Folder

```text
.
├── cmd/
│   ├── server/               # Menjalankan server, mengatur route, middleware, dan handler HTTP.
│   ├── worker/               # Binary worker terpisah untuk memproses antrean migration async.
│   ├── bench-migration/      # Alat benchmark mode normal vs batch pada dataset besar.
│   └── seed-admin/           # Program untuk membuat atau memperbarui akun Platform Admin.
├── internal/
│   ├── admin/                # Pengelolaan user, group, pemberian akses, dan audit log.
│   ├── async/                # Async engine: antrean PostgreSQL, lease lock, executor batch, worker.
│   ├── auth/                 # Login, password, session, dan perlindungan CSRF.
│   ├── authz/                # Pemeriksaan hak akses berdasarkan keanggotaan group.
│   ├── explain/              # Menjalankan dan membaca hasil EXPLAIN/ANALYZE.
│   ├── migration/            # Draft SQL, validasi, eksekusi, transaksi, dan riwayat migration.
│   ├── store/                # Akses database, konfigurasi koneksi, enkripsi, dan database viewer.
│   │   ├── migrations/       # SQL untuk menyiapkan tabel internal aplikasi di PostgreSQL.
│   │   └── migrations-mariadb/ # SQL untuk membuat tabel internal aplikasi dalam format MariaDB.
│   ├── telemetry/            # Structured JSON logger, OpenTelemetry tracing, dan redaksi data sensitif.
│   └── web/
│       ├── templates.go      # Registrasi dan pemuatan template aplikasi.
│       ├── templates/        # Halaman aplikasi menggunakan Go template.
│       └── static/           # File CSS untuk tampilan aplikasi.
├── frontend/                 # Single Page Application React (MigrationScreen, API polling).
├── docs/                     # Dokumentasi desain (async-engine-observability.md).
├── scripts/                  # Script SQL benchmark dataset (generate_series > 1 detik).
├── test/                     # Pengujian terpisah dari package implementasi.
│   ├── async/                # Integration test async engine (lock, resume, revert, duplikat).
│   ├── auth/                 # Pengujian autentikasi dan redaksi data sensitif.
│   ├── authz/                # Pengujian permission dan otorisasi.
│   ├── explain/              # Pengujian fitur EXPLAIN/ANALYZE.
│   ├── migration/            # Pengujian validasi, service, store, dan eksekusi migration.
│   └── store/                # Pengujian koneksi dan query database.
├── assets/                   # Aset dokumentasi, termasuk gambar ERD.
├── Dockerfile                # Multi-stage build untuk binary server dan worker.
├── frontend/Dockerfile       # Build SPA React + nginx.
├── .env.example             # Contoh konfigurasi environment.
├── docker-compose.yml       # Konfigurasi container postgres, server, worker, frontend.
├── go.mod                   # Nama module, versi Go, dan dependency proyek.
├── go.sum                   # Checksum dependency Go.
└── README.md                # Informasi proyek dan cara menjalankannya.
```

Pengujian berada di folder `test/` dan dikelompokkan berdasarkan fitur yang diuji. File
`compat.go` pada beberapa subfolder membantu pengujian tetap kompatibel dengan konfigurasi
database yang digunakan.

## ERD Model Relasional

ERD berikut menunjukkan model relasional database Harrow:

![ERD Model Relasional](assets/Model_Relasional.png)

Relasi utamanya adalah:

- `users` menyimpan akun pengguna, sedangkan `sessions` menyimpan sesi login yang terkait dengan setiap user.
- `users` dan `user_groups` terhubung secara many-to-many melalui `user_group_members`.
- `user_groups` dan `database_connections` juga terhubung secara many-to-many melalui `group_database_access`. Tabel ini menyimpan level akses `View` atau `View + Edit`.
- Hak akses aplikasi dikelola melalui `roles`, `permissions`, `role_permissions`, dan `user_roles`.
- `audit_logs` mencatat aktivitas pengguna terhadap resource aplikasi atau koneksi database.

## Cara Menjalankan

Ikuti langkah-langkah berikut secara berurutan untuk menjalankan aplikasi Harrow hingga UI muncul di web browser.

### Prasyarat

Pastikan Anda telah menginstall:
- **Docker** dan **Docker Compose** (untuk menjalankan database PostgreSQL)
- **Go** versi 1.27.3 atau lebih tinggi
- **Node.js** dan **npm** (untuk menjalankan frontend React)

### 1. Nyalakan Docker

Buka Docker Desktop atau jalankan Docker Engine, lalu tunggu sampai siap digunakan.

### 2. Siapkan Konfigurasi Environment

Jika belum memiliki file `.env`, salin contoh konfigurasi:

```bash
cp .env.example .env
```

Edit file `.env` dan isi variabel-variabel berikut:

```bash
# Database PostgreSQL
POSTGRES_DB=harrow_db
POSTGRES_USER=harrow
POSTGRES_PASSWORD=your_secure_password_here
POSTGRES_PORT=5433

# Encryption key untuk keamanan data
ENCRYPTION_KEY=your_encryption_key_here

# Port aplikasi
APP_PORT=8080
```

**Cara membuat `ENCRYPTION_KEY`:**

```bash
openssl rand -hex 32
```

Salin hasilnya ke variabel `ENCRYPTION_KEY` di file `.env`.

> **Catatan:** Jika sudah ada `ENCRYPTION_KEY` yang digunakan sebelumnya, gunakan key tersebut agar data koneksi lama tetap dapat dibuka.

### 3. Jalankan Database PostgreSQL

Jalankan container PostgreSQL menggunakan Docker Compose:

```bash
docker compose up -d postgres
```

Tunggu hingga database siap (biasanya 5-10 detik). Verifikasi container berjalan:

```bash
docker compose ps
```

Anda akan melihat output seperti:
```
NAME          IMAGE         STATUS                   PORTS
harrow-postgres-1   postgres:18   Up (healthy)   0.0.0.0:5433->5432/tcp
```

### 4. Buat Akun Admin (Penggunaan Pertama Kali)

Jika ini pertama kali menjalankan aplikasi, buat akun admin:

```bash
go run ./cmd/server seed-admin admin
```

Sistem akan meminta password untuk akun admin. Masukkan password yang diinginkan lalu tekan Enter.

> **Tips:** Anda juga dapat set password via environment variable:
> ```bash
> SEED_ADMIN_PASSWORD=your_password go run ./cmd/server seed-admin admin
> ```

### 5. Jalankan API Server

Buka terminal baru dan jalankan server:

```bash
go run ./cmd/server
```

Server akan dimulai dan menampilkan log:
```
2026/11/26 10:30:00 server.started port=8080 status=listening
```

### 6. Jalankan Worker (Async Migration Processor)

Buka terminal baru lagi dan jalankan worker:

```bash
go run ./cmd/worker
```

Worker akan dimulai dan menampilkan log:
```
{"time":"2026-11-26T10:30:00Z","level":"INFO","event":"worker.started","worker_id":"worker-xxx","concurrency":4,"status":"ready"}
```

### 7. Jalankan Frontend (Development Mode)

Buka terminal baru, masuk ke folder frontend, dan jalankan:

```bash
cd frontend
npm install    # Hanya diperlukan pertama kali
npm run dev
```

Frontend akan berjalan di `http://localhost:5173` (default Vite dev server).

> **Alternatif - Mode Production:** Untuk build production dan serve via nginx:
> ```bash
> docker compose up -d frontend
> ```
> Frontend akan tersedia di `http://localhost:5173` secara default (atau menyesuaikan dengan `FRONTEND_PORT`).

### 8. Akses Aplikasi di Browser

Buka web browser dan akses:

**Development Mode:**
- Frontend: [http://localhost:5173](http://localhost:5173)
- API Server: [http://localhost:8080](http://localhost:8080)

**Production Mode (Docker):**
- Frontend: [http://localhost:5173](http://localhost:5173)
- API Server: [http://localhost:8080](http://localhost:8080)

Login menggunakan akun admin yang telah dibuat di langkah 4.

### Peran dan Hak Akses

| Peran | Keterangan |
| --- | --- |
| **Platform Admin** | Mengelola user, group, koneksi database, dan pemberian akses |
| **User** | Mengakses database melalui keanggotaan group |
| **View + Edit** | Melihat schema/data, membuat dan menjalankan migration |
| **View Only** | Melihat schema/data dan riwayat migration (read-only) |

## Bonus: Stress Testing dengan k6

[Script k6](test/k6/stress.js) menjalankan `GET` daftar migration dan `POST` eksekusi migration secara bersamaan. Setiap VU login, mengambil satu fixture per iterasi, dan polling job yang diterima sampai status terminal. `same_database` memakai satu database PostgreSQL. `different_databases` bergantian memakai dua database PostgreSQL.

Beban normal awal adalah 2 virtual user (VU). Tes naik ke 4 lalu 8 VU, turun ke 4 lalu 2 VU, kemudian berhenti. Setiap tingkat naik dan pemulihan punya fase stabil tersendiri. Default setiap fase 30 detik. Atur `K6_NORMAL_VUS` sesuai beban normal tim dan `K6_STAGE_SECONDS` untuk mengubah durasi per fase.

### Menjalankan pengujian

1. Siapkan lingkungan lokal khusus pengujian. Jalankan server dan worker beserta PostgreSQL. Dari folder `frontend`, jalankan Vite sebagai proxy API.

   ~~~bash
   docker compose up -d --build postgres server worker
   cd frontend
   npm run dev -- --host 127.0.0.1
   ~~~

2. Tambahkan dua koneksi ke **nama database PostgreSQL berbeda**. Untuk server dan worker di Docker, gunakan host `postgres` dan port `5432` jika database target berada di layanan tersebut. Beri akun uji akses **View + Edit** di kedua koneksi. Dari root repo, ganti `first_db` dan `second_db` dengan ID koneksi uji.

   ~~~bash
   docker compose cp test/k6/seed.sql postgres:/tmp/k6-seed.sql
   docker compose exec -T postgres psql -U harrow -d harrow_db -v commit_seed=true -v first_db=3 -v second_db=8 -f /tmp/k6-seed.sql
   docker compose cp postgres:/tmp/k6-fixtures.json test/k6/fixtures.local.json
   ~~~

   Seed menyimpan migration, revisi, dan approval di database aplikasi. SQL migration adalah `SELECT 1;`. Defaultnya 1.300 fixture per skenario untuk durasi 30 detik dan 2 VU normal. Sediakan fixture baru untuk setiap pengulangan skenario. Naikkan `-v same_count=N` dan `-v different_count=N` jika jumlah VU atau durasi bertambah. `different_count` berlaku per database.

3. Dari root repo, jalankan kedua skenario. Contoh memakai Bash atau Zsh. Simpan output k6 dan ringkasan per fase.

   ~~~bash
   export BASE_URL=http://127.0.0.1:5173 K6_USERNAME=akun-test K6_NORMAL_VUS=2
   read -s K6_PASSWORD
   export K6_PASSWORD

   K6_CASE=same_database k6 run --out json=test/k6/results-after-same.json test/k6/stress.js
   K6_CASE=different_databases k6 run --out json=test/k6/results-after-different.json test/k6/stress.js
   python3 test/k6/report.py test/k6/results-after-same.json test/k6/results-after-different.json
   unset K6_PASSWORD
   ~~~

### Hasil pengujian

Run menggunakan 2 VU normal (maksimum 8 VU) dengan 8 detik per fase. Semua job yang diterima selesai sukses dan kedua skenario lulus threshold p95 API. Skenario database sama gagal threshold error API 1% karena respons HTTP 409.

| Skenario | API req/s | API p50 / p95 | Error API | Job sukses | Queue wait p95 | Durasi job p95 | HTTP 409 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Database sama | 6.97 | 4.49 / 13.48 ms | 4.40% | 137/137 (100%) | 6.00 ms | 13.20 ms | 27 |
| Database berbeda | 7.60 | 4.05 / 12.46 ms | 0.45% | 165/165 (100%) | 4.00 ms | 12.00 ms | 3 |

Throughput per tingkat beban dihitung per jendela 8 detik. Durasi job dihitung dari saat masuk antrean hingga selesai.

| Tahap | API p50 / p95 | API req/s | Error | Job sukses/s | Queue wait p95 | Durasi job p95 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Sama — normal | 4.97 / 11.79 ms | 4.12 | 0.00% | 1.00 | 6.00 ms | 16.25 ms |
| Sama — 2x | 4.16 / 8.37 ms | 8.12 | 0.00% | 2.00 | 3.00 ms | 10.25 ms |
| Sama — 4x | 4.54 / 13.33 ms | 14.12 | 7.96% | 2.88 | 3.00 ms | 10.90 ms |
| Sama — pemulihan 2x | 4.60 / 11.14 ms | 7.75 | 3.23% | 1.75 | 3.00 ms | 11.00 ms |
| Sama — pemulihan normal | 4.81 / 8.95 ms | 3.50 | 14.29% | 0.62 | 3.00 ms | 9.80 ms |
| Berbeda — normal | 4.29 / 9.38 ms | 4.12 | 0.00% | 1.00 | 4.95 ms | 12.60 ms |
| Berbeda — 2x | 3.79 / 12.92 ms | 8.12 | 0.00% | 2.00 | 4.25 ms | 12.25 ms |
| Berbeda — 4x | 3.99 / 12.23 ms | 16.00 | 1.56% | 3.88 | 4.50 ms | 11.50 ms |
| Berbeda — pemulihan 2x | 3.85 / 8.11 ms | 8.75 | 0.00% | 2.25 | 4.15 ms | 12.75 ms |
| Berbeda — pemulihan normal | 4.09 / 9.30 ms | 4.50 | 0.00% | 1.12 | 3.00 ms | 10.60 ms |

P95 tetap di bawah target 1 detik pada semua tingkat beban dan kembali ke sekitar 9 ms saat beban turun ke normal. Queue wait p95 keseluruhan 4–6 ms. Konflik 409 berjumlah 27 pada skenario database sama dan 3 pada skenario database berbeda. Konflik masih muncul pada fase pemulihan database sama.

Setelah penambahan indeks approval, p95 khusus endpoint daftar migration turun dari 53.29 ke 15.78 ms pada database sama dan dari 52.33 ke 13.69 ms pada database berbeda. Job sukses per detik turun dalam pengujian ini, seiring dengan bertambahnya polling ulang yang berjeda satu detik. [Migration indeks approval](internal/store/migrations/000006_latest_approval_index.up.sql)

## Menghentikan Aplikasi

### 1. Hentikan Frontend

Pada terminal frontend:
- **Development mode:** Tekan `Ctrl+C`
- **Production mode (Docker):** 
  ```bash
  docker compose stop frontend
  ```

### 2. Hentikan Worker

Pada terminal worker, tekan `Ctrl+C` untuk menghentikan worker secara graceful.

### 3. Hentikan API Server

Pada terminal server, tekan `Ctrl+C` untuk menghentikan server.

### 4. Hentikan Database (Optional)

Jika ingin menghentikan database PostgreSQL:

```bash
docker compose stop postgres
```

Untuk menghentikan semua container termasuk MariaDB (jika dijalankan):

```bash
docker compose --profile mariadb stop
```

> **Catatan:** Data database tetap tersimpan di Docker volume dan akan tersedia kembali saat container dijalankan ulang.

### 5. Hapus Semua Container dan Volume (Reset Total)

Jika ingin menghapus semua data dan memulai dari awal:

```bash
docker compose --profile mariadb down -v
```

> **Perintah ini akan menghapus semua data database, termasuk user, group, dan migration history**
