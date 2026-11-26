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

Script `test/k6/stress.js` menguji API dan eksekusi migration secara bersamaan. Beban naik dari normal ke 2x dan 4x, lalu turun untuk mengukur pemulihan. Dua skenario:
- `same_database`: semua job memakai satu database.
- `different_databases`: job berjalan pada dua database PostgreSQL yang berbeda.

### Menjalankan test

1. Pastikan k6 terpasang, lalu jalankan PostgreSQL, server, dan worker:

   ```powershell
   docker compose up -d postgres server worker
   ```

   Di terminal lain, jalankan frontend:

   ```powershell
   cd frontend
   npm.cmd run dev -- --host 127.0.0.1
   ```

2. Siapkan dua target PostgreSQL yang berbeda secara fisik dan beri akun test akses `View + Edit`. Worker tidak mendukung migration ke MariaDB. Pastikan host koneksi dapat dijangkau dari server (`postgres:5432` jika server/worker di Docker; `127.0.0.1:5433` jika berjalan di host). Pada setup lokal yang dipakai untuk hasil di bawah, targetnya adalah database ID `3` dan `8`. Jika ID berbeda, sesuaikan `test/k6/seed.sql`.

3. Buat fixture migration sekali pakai dan approved:

   ```powershell
   docker compose cp test\k6\seed.sql postgres:/tmp/k6-seed.sql
   docker compose exec -T postgres psql -U harrow -d harrow_db -v commit_seed=true -f /tmp/k6-seed.sql
   docker compose cp postgres:/tmp/k6-fixtures.json test\k6\fixtures.local.json
   docker compose exec -T postgres rm -f /tmp/k6-seed.sql /tmp/k6-fixtures.json
   ```

   Seed memakai `SELECT 1;` sehingga tidak mengubah data target, tetapi menambah record permanen ke database aplikasi. Seed membuat migration approved langsung; ini menguji enqueue dan worker, bukan alur review. Jalankan ulang seed sebelum test berikutnya karena setiap migration hanya boleh dipakai sekali.

4. Di PowerShell, atur kredensial akun test dan jalankan kedua skenario:

   ```powershell
   $env:BASE_URL = "http://127.0.0.1:5173"
   $env:K6_USERNAME = "akun-test"
   $env:K6_PASSWORD = "password-test"
   $env:K6_NORMAL_VUS = "2"

   $env:K6_CASE = "same_database"
   k6 run --summary-export=test/k6/results-same-database.json test/k6/stress.js

   $env:K6_CASE = "different_databases"
   k6 run --summary-export=test/k6/results-different-databases.json test/k6/stress.js
   ```

Default threshold: API p95 `< 1000 ms`, error API `< 1%`, dan job sukses `> 95%`. `202 Accepted` hanya berarti job diterima; job dihitung sukses setelah status terminal berhasil. HTTP 409 dicatat terpisah sebagai konflik enqueue dan tidak masuk ke metrik error API.

### Hasil pengujian

Run menggunakan 2 VU normal (maksimum 8 VU). Semua job yang diterima selesai sukses, tetapi kedua skenario **gagal threshold p95 API**.

| Skenario | API req/s | API p50 / p95 | Error API | Job sukses | Queue wait p95 | Durasi job p95 | HTTP 409 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Database sama | 2.042 | 43.5 / 5158.6 ms | 0% | 134/134 (100%) | 25.3 ms | 1099.1 ms | 68 |
| Database berbeda | 3.538 | 25.6 / 4268.0 ms | 0% | 286/286 (100%) | 23 ms | 1066.8 ms | 42 |

Per tingkat beban (throughput dihitung per jendela 30 detik):

| Tahap | API p50 / p95 | API req/s | Error | Job sukses/s | Queue wait p95 | Durasi job p95 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Sama — normal | 38.1 / 2626.3 ms | 1.833 | 0% | 0.567 | 15.8 ms | 1070.4 ms |
| Sama — 2x | 39.9 / 3473.6 ms | 2.400 | 0% | 0.533 | 21.2 ms | 1074.2 ms |
| Sama — 4x | 54.0 / 5340.8 ms | 3.167 | 0% | 0.633 | 27.1 ms | 1104.4 ms |
| Sama — pemulihan 2x | 47.0 / 5244.2 ms | 2.633 | 0% | 0.567 | 26.2 ms | 1129 ms |
| Sama — pemulihan normal | 42.0 / 3412 ms | 2.133 | 0% | 0.600 | 17.7 ms | 1068.5 ms |
| Berbeda — normal | 19.3 / 2464.2 ms | 2.667 | 0% | 0.867 | 15.2 ms | 1048.5 ms |
| Berbeda — 2x | 22.6 / 3245.2 ms | 4.067 | 0% | 1.200 | 20.2 ms | 1055.8 ms |
| Berbeda — 4x | 31.1 / 4873.1 ms | 5.633 | 0% | 1.567 | 28.4 ms | 1077.6 ms |
| Berbeda — pemulihan 2x | 29.6 / 4646.7 ms | 5.167 | 0% | 1.500 | 24.6 ms | 1073.6 ms |
| Berbeda — pemulihan normal | 22.4 / 3016.3 ms | 3.600 | 0% | 1.133 | 18 ms | 1053.8 ms |

P95 meningkat saat beban naik dan turun saat pemulihan, tetapi tetap di atas target 1 detik. Queue wait p95 hanya 23–25 ms; hasil ini menunjukkan keterlambatan utama ada pada respons API, bukan antrean worker. Konflik 409 berjumlah 68 pada skenario satu database dan 42 pada dua database.

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
