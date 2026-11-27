# HarrowDotCom

**Kode Kelompok: K02-11**

**HarrowDotCom** adalah platform manajemen koneksi database, eksplorasi skema/data (*Database Explorer*), analisis rencana eksekusi query (`EXPLAIN` / `EXPLAIN ANALYZE`), serta manajemen dan eksekusi migrasi SQL sinkron maupun asinkron (*normal* & *cursor-based batch mode*) yang dikembangkan untuk Tugas Besar **IF3110 Pengembangan Aplikasi Web**.

### Anggota Kelompok

| NIM | Nama Lengkap |
| --- | --- |
| 13524005 | Geraldo Artemius |
| 13524080 | Renuno Yuqa Frinardi |
| 13524104 | Valentino Daniel Kusumo |

---

## Struktur Folder

```text
.
├── .github/workflows/
│   └── codeql.yml            # Workflow GitHub Actions untuk analisis statis (SAST) CodeQL.
├── assets/                   # Aset gambar dokumentasi.
├── cmd/
│   ├── server/               # HTTP API server, routing, middleware, dan autentikasi/otorisasi.
│   ├── worker/               # Worker asinkron pemroses antrean job migrasi & rollback batch.
│   ├── seed-admin/           # CLI pembuatan/pembaruan akun Platform Admin awal.
│   ├── seed-target-db/       # CLI verifikasi & registrasi otomatis koneksi database commerce-db.
│   └── bench-migration/      # Alat benchmark eksekusi migrasi mode normal vs batch.
├── docs/
│   └── codeql.md             # Dokumentasi cakupan dan bukti hasil pemindaian CodeQL.
├── frontend/                 # Single Page Application React + Vite (dan konfigurasi Nginx).
├── internal/
│   ├── admin/                # Pengelolaan user, group, hak akses database, dan audit log.
│   ├── async/                # Async engine: antrean PostgreSQL, lease lock, retry, batch executor.
│   ├── auth/                 # Login, hashing password, manajemen sesi, dan proteksi CSRF.
│   ├── authz/                # Otorisasi berbasis role dan keanggotaan group.
│   ├── explain/              # Eksekusi dan pembacaan hasil EXPLAIN / EXPLAIN ANALYZE.
│   ├── migration/            # Validasi SQL, workflow approval, revisi, dan eksekusi migrasi.
│   ├── store/                # Koneksi database, enkripsi AES-GCM, migrasi internal, dan explorer.
│   ├── telemetry/            # Structured JSON logger, OpenTelemetry tracing, dan redaksi rahasia.
│   └── web/                  # Template halaman dan aset statis web.
├── scripts/
│   ├── benchmark_dataset.sql # Script dataset benchmark ledger sederhana.
│   └── commerce-db/          # Automatic seeding DDL/DML (~1,6 juta baris) & contoh query beban.
├── test/                     # Unit test, integration test per modul, dan stress test k6.
├── Dockerfile                # Multi-stage build untuk binary server, worker, dan seeder.
├── docker-compose.yml        # Orkestrasi container postgres, commerce-db, server, worker, frontend.
├── .env.example              # Contoh konfigurasi variabel environment terpusat.
└── README.md                 # Dokumentasi utama proyek.
```

---

## Database Target `commerce_db` (`harrow_commerce`)

Untuk menguji performa query berat, migrasi batch asinkron, dan *load balancing* sistem secara realistis, proyek ini menyediakan container PostgreSQL 18 terpisah bernama **`commerce-db`** (`database: harrow_commerce`). Saat container pertama kali dijalankan, PostgreSQL secara otomatis menjalankan *seeding* melalui `/docker-entrypoint-initdb.d/` (`scripts/commerce-db/initdb/`) dan layanan `commerce-db-init` mendaftarkannya langsung ke HarrowDotCom sebagai koneksi **`Commerce LoadTest DB`**.

Karakteristik utama dataset (`~1.601.210` baris / `~510 MB`):
- **4 Schema & 14 Tabel Hybrid (Relasional + `JSONB` + `TEXT[]`)**:
  - `public`: `regions` (10), `merchants` (2.000), `customers` (50.000), `settlement_batches` (300.000 — siap diuji dengan migrasi *cursor-batch*).
  - `catalog`: `categories` (120, hierarki 3 level), `warehouses` (60), `products` (25.000), `inventory_levels` (100.000).
  - `orders`: `orders` (150.000, distribusi *Pareto/skewed*), `order_items` (450.000), `shipments` (120.000).
  - `finance`: `ledger_accounts` (4.020), `journal_entries` (300.000, *double-entry*), `fraud_signals` (100.000).
- **Integritas & Indeks**: 24 *Foreign Key* lintas schema, 10 *CHECK constraint*, 13 *UNIQUE constraint*, 26 indeks sekunder (*B-Tree*, *Partial*, *GIN*, *BRIN*), serta 2 *analytical view*.

### ERD `commerce_db`

```mermaid
erDiagram
    REGIONS ||--o{ MERCHANTS : "hosts"
    REGIONS ||--o{ CUSTOMERS : "serves"
    REGIONS ||--o{ SETTLEMENT_BATCHES : "settles_in"
    REGIONS ||--o{ WAREHOUSES : "locates"
    REGIONS ||--o{ ORDERS : "originates"

    MERCHANTS ||--o{ SETTLEMENT_BATCHES : "receives"
    MERCHANTS ||--o{ PRODUCTS : "sells"
    MERCHANTS ||--o{ ORDER_ITEMS : "fulfills"
    MERCHANTS ||--o{ LEDGER_ACCOUNTS : "owns"

    CATEGORIES ||--o{ CATEGORIES : "parent_of"
    CATEGORIES ||--o{ PRODUCTS : "classifies"

    WAREHOUSES ||--o{ INVENTORY_LEVELS : "stores"
    WAREHOUSES ||--o{ ORDERS : "dispatches"
    WAREHOUSES ||--o{ SHIPMENTS : "ships_from"

    PRODUCTS ||--o{ INVENTORY_LEVELS : "stocked_in"
    PRODUCTS ||--o{ ORDER_ITEMS : "ordered_as"

    CUSTOMERS ||--o{ ORDERS : "places"
    CUSTOMERS ||--o{ FRAUD_SIGNALS : "evaluated_for"

    ORDERS ||--|{ ORDER_ITEMS : "contains"
    ORDERS ||--o{ SHIPMENTS : "tracked_by"
    ORDERS ||--o{ JOURNAL_ENTRIES : "posts"
    ORDERS ||--o{ FRAUD_SIGNALS : "triggers"

    LEDGER_ACCOUNTS ||--o{ JOURNAL_ENTRIES : "debits"
    LEDGER_ACCOUNTS ||--o{ JOURNAL_ENTRIES : "credits"

    REGIONS {
        smallint id PK
        varchar code UK
        char currency
        numeric tax_rate
    }
    MERCHANTS {
        bigint id PK
        smallint region_id FK
        varchar merchant_code UK
        varchar tier
        jsonb storefront_config
    }
    CUSTOMERS {
        bigint id PK
        smallint region_id FK
        uuid external_uuid UK
        varchar email UK
        varchar loyalty_tier
        jsonb preferences
    }
    SETTLEMENT_BATCHES {
        bigint id PK
        bigint merchant_id FK
        smallint region_id FK
        varchar batch_code UK
        numeric net_amount
        boolean reconciled
    }
    CATEGORIES {
        int id PK
        int parent_id FK
        varchar slug UK
        smallint depth_level
    }
    WAREHOUSES {
        int id PK
        smallint region_id FK
        varchar warehouse_code UK
        varchar tier
    }
    PRODUCTS {
        bigint id PK
        bigint merchant_id FK
        int category_id FK
        varchar sku UK
        numeric base_price
        jsonb specs
    }
    INVENTORY_LEVELS {
        bigint id PK
        int warehouse_id FK
        bigint product_id FK
        int on_hand_qty
        int reserved_qty
    }
    ORDERS {
        bigint id PK
        bigint customer_id FK
        smallint region_id FK
        int warehouse_id FK
        varchar order_number UK
        numeric total_amount
        timestamptz placed_at
    }
    ORDER_ITEMS {
        bigint id PK
        bigint order_id FK
        bigint product_id FK
        bigint merchant_id FK
        int quantity
        numeric line_total
    }
    SHIPMENTS {
        bigint id PK
        bigint order_id FK
        int warehouse_id FK
        varchar tracking_number UK
        varchar shipment_status
    }
    LEDGER_ACCOUNTS {
        bigint id PK
        bigint merchant_id FK
        varchar account_code UK
        varchar account_type
        numeric current_balance
    }
    JOURNAL_ENTRIES {
        bigint id PK
        bigint order_id FK
        bigint debit_account_id FK
        bigint credit_account_id FK
        varchar reference_code UK
        numeric amount
    }
    FRAUD_SIGNALS {
        bigint id PK
        bigint order_id FK
        bigint customer_id FK
        numeric risk_score
        varchar verdict
    }
```

---

## Cara Menjalankan

### 1. Prasyarat
- **Docker** & **Docker Compose**
- **Go** `>= 1.27.3` serta **Node.js** & **npm** *(hanya diperlukan jika menjalankan secara manual di luar Docker)*

### 2. Konfigurasi `.env`
Salin template konfigurasi dan isi `ENCRYPTION_KEY` (32 byte hex) serta password yang diinginkan:

```bash
cp .env.example .env
openssl rand -hex 32   # Salin output ke variabel ENCRYPTION_KEY di dalam .env
```

### 3. Menjalankan dengan Docker (Direkomendasikan)
Menjalankan database utama (`postgres`), database target + *auto-seeding* (`commerce-db` & `commerce-db-init`), `server`, `worker`, dan `frontend`:

```bash
docker compose up -d --build
docker compose exec server harrow-server seed-admin
```

Akses aplikasi pada `http://localhost:5173` (atau sesuai `FRONTEND_PORT` di `.env`).

### 4. Menjalankan secara Manual (Development Mode)
Jalankan container database terlebih dahulu, lalu jalankan layanan aplikasi di terminal terpisah:

```bash
# Terminal 1: Jalankan PostgreSQL utama & commerce-db, lalu seed akun admin & koneksi target
docker compose up -d postgres commerce-db
go run ./cmd/seed-admin
go run ./cmd/seed-target-db

# Terminal 2: Jalankan API Server
go run ./cmd/server

# Terminal 3: Jalankan Async Worker
go run ./cmd/worker

# Terminal 4: Jalankan Frontend Vite
cd frontend && npm install && npm run dev
```

### 5. Cara Menghentikan (How to Stop)
- **Mode Manual**: Tekan `Ctrl+C` pada terminal `frontend`, `worker`, dan `server`, lalu hentikan database dengan `docker compose stop postgres commerce-db`.
- **Mode Docker**:
  ```bash
  # Menghentikan seluruh container (data tetap tersimpan di volume)
  docker compose --profile mariadb stop

  # Menghentikan sekaligus menghapus seluruh container dan volume database (reset total)
  docker compose --profile mariadb down -v
  ```

---

## Tampilan Aplikasi

### Login
![Halaman login](assets/login.png)

### Dashboard
![Dashboard](assets/dashboard.png)

### Koneksi PostgreSQL
![Halaman koneksi PostgreSQL](assets/connections.png)

### Users
![Halaman users](assets/users.png)

### Groups
![Halaman groups](assets/groups.png)

### Akses Database
![Halaman akses database](assets/access.png)

### Audit Logs
![Halaman audit logs](assets/audit_logs.png)

## Pengujian Beban dengan k6

k6 adalah alat *load testing* yang mensimulasikan banyak pengguna virtual (*virtual users/VUs*) untuk melihat respons aplikasi saat menerima beban. Pada proyek ini, [`test/k6/stress.js`](test/k6/stress.js) menguji alur migrasi asinkron: login, mengambil daftar migrasi, mengajukan migrasi yang sudah disetujui, lalu memantau status job sampai selesai.

### Skenario

Jumlah VUs dinaikkan bertahap dari 0 ke jumlah normal, 2x, lalu 4x, sebelum diturunkan kembali untuk mengamati pemulihan dan penyelesaian antrean. Secara default jumlah normal adalah **2 VUs** dan setiap tahap berlangsung **30 detik**. Pengujian bisa memilih salah satu skenario berikut:
- `same_database`: beberapa job berjalan pada satu database target, untuk melihat perilaku antrean pada database yang sama.
- `different_databases`: job dijalankan pada beberapa database target, untuk melihat perilaku sistem ketika beban tersebar.

### Konfigurasi dan persiapan

Sebelum menjalankan pengujian, aplikasi harus aktif di komputer lokal, k6 harus terpasang, dan file `fixtures.local.json` harus tersedia di folder `test/k6`. File fixture berisi ID migrasi yang sudah disetujui dan belum pernah dipakai; [`fixtures.example.json`](test/k6/fixtures.example.json) menunjukkan formatnya. Siapkan juga akun pengujian melalui variabel lingkungan berikut:

| Variabel | Kegunaan | Nilai default |
| --- | --- | --- |
| `K6_USERNAME` / `K6_PASSWORD` | Kredensial untuk login ke aplikasi | Wajib diisi |
| `K6_CASE` | Skenario yang dijalankan: `same_database` atau `different_databases` | `same_database` |
| `K6_NORMAL_VUS` | Jumlah VUs pada beban normal; puncak pengujian mencapai 4x nilai ini | `2` |
| `K6_STAGE_SECONDS` | Durasi setiap tahap kenaikan/penurunan beban dalam detik | `30` |

Setelah persiapan, jalankan `k6 run stress.js` dari folder `test/k6`. Skrip hanya mengizinkan target `localhost`/`127.0.0.1`; jalankan pengujian pada lingkungan lokal dengan data uji, bukan pada layanan produksi.

### Hasil yang diperiksa

k6 merangkum waktu respons API, tingkat error, jumlah dan keberhasilan job, serta waktu antre dan waktu penyelesaian. Pengujian dianggap lulus jika *threshold* yang ditentukan skrip terpenuhi, yaitu p95 respons API di bawah **1 detik**, error API di bawah **1%**, keberhasilan job di atas **95%**, tidak ada job timeout atau fixture habis, serta ada job yang berhasil diajukan dan mencapai status akhir.

## Implementasi SAST dengan CodeQL

Proyek ini mengimplementasikan *Static Application Security Testing* (**SAST**) otomatis menggunakan **GitHub CodeQL** yang dikonfigurasi pada [`.github/workflows/codeql.yml`](.github/workflows/codeql.yml):
- **Trigger Otomatis**: Berjalan pada setiap `push` dan `pull_request` ke branch `main` maupun `develop`, serta mendukung eksekusi manual (`workflow_dispatch`) dengan izin minimal (`contents: read`, `security-events: write`).
- **Cakupan Multi-Bahasa**:
  - **Go (`build-mode: manual`)**: Mengekstraksi dan menganalisis seluruh paket Go (`server`, `worker`, seeder, dan seluruh package di `internal/`) melalui kompilasi penuh `CGO_ENABLED=1 go build -mod=readonly ./...` sesuai versi Go pada `go.mod`.
  - **JavaScript/TypeScript (`build-mode: none`)**: Menganalisis kode sumber frontend React (`frontend/src/`) dan skrip JavaScript di repositori secara statis.
- **Dokumentasi & Bukti Pemindaian**: Riwayat pemindaian PR, tautan *GitHub Security Code Scanning*, dan tinjauan temuan (0 *open alerts*) terdokumentasi di [`docs/codeql.md`](docs/codeql.md).
