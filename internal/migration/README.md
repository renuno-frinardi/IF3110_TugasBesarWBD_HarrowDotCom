# Eksekusi migration

Pemanggilan dari backend:

```go
result, err := service.Execute(ctx, userID, migrationID, hexKey)
```

`userID` harus berasal dari session yang sudah diverifikasi. `hexKey` berasal
dari konfigurasi server (`ENCRYPTION_KEY`), bukan dari request pengguna.
Gunakan context request agar eksekusi bisa dibatalkan. `result` dapat berisi
riwayat berstatus `failed` walaupun `err` tidak nil.

## Alur execute.go

1. `Execute` memeriksa izin `view_edit` dan status draft.
2. Koneksi khusus ke database aplikasi memegang advisory lock berdasarkan
   `DatabaseID`. Permintaan berikutnya pada ID yang sama menunggu giliran.
3. `claimMigration` mengubah draft menjadi `running`, menetapkan versi dan
   pelaksana, lalu mengambil SQL terbaru dalam satu UPDATE. SQL ini divalidasi
   sebelum perubahan metadata di-commit. SQL yang ditolak tetap menjadi draft.
4. `MakeConnection` membuka database target. `runMigrationSQL` menjalankan
   seluruh script dalam satu transaksi, kemudian commit atau rollback.
5. `saveExecution` mencatat status, waktu selesai, dan pesan error. Pencatatan
   serta cleanup memakai context tersendiri agar tetap berjalan saat request batal.

Versi adalah urutan percobaan eksekusi per `DatabaseID`. Draft belum memiliki
versi. Percobaan gagal setelah status menjadi `running` tetap menggunakan
nomor versinya. Untuk memperbaiki migration yang sudah dijalankan, buat draft baru.

`NotDraft` menandakan migration sudah bukan draft. `ExecutionPending` menandakan
ada hasil sebelumnya yang perlu diperiksa. Keduanya bisa diperiksa dengan `errors.Is`.

## Hasil yang belum pasti

Transaksi target dan pencatatan riwayat berada pada dua koneksi/database berbeda;
keduanya tidak otomatis menjadi satu transaksi atomik.

- Jika konfirmasi commit terputus, status tetap `running` dan SQL tidak dicoba ulang.
- Jika commit berhasil tetapi pencatatan gagal, perubahan target tetap ada. Error
  menjelaskan bahwa commit sudah berhasil; riwayat yang masih `running` menahan
  eksekusi berikutnya.
- Jika proses mati, riwayat `running` juga menahan eksekusi berikutnya. Operator
  perlu memeriksa perubahan pada target dan bukti hasil transaksi, lalu memperbaiki
  status riwayat menjadi `executed` atau `failed` sesuai hasil yang sudah dipastikan.
  Jangan mengubahnya kembali menjadi draft atau menjalankan SQL ulang hanya karena
  statusnya masih `running`. Pemulihan otomatis belum disediakan.

Lock dan versi berlaku per ID koneksi. Daftarkan satu database fisik satu kali;
dua ID yang menunjuk database fisik sama tidak berbagi urutan versi/lock.
Validator bukan sandbox: efek eksternal fungsi dan operasi yang tidak bisa
dibatalkan oleh PostgreSQL tidak dijamin oleh transaksi ini. Parser v6 memakai
grammar PostgreSQL 17; sintaks khusus PostgreSQL 18 bisa ditolak parser.

## Pengujian

```sh
go test ./internal/store ./internal/migration -count=1
```

`TEST_DATABASE_URL` harus menunjuk database aplikasi khusus tes dengan nama
berakhiran `_test` dan schema terbaru yang sudah diterapkan. Jangan arahkan ke
database aplikasi utama. Tes tidak menerapkan ulang migration schema aplikasi.

Tes executor membuat database target sementara bernama `execute_<angka>_test`,
kemudian menghapusnya saat selesai. Akun tes memerlukan izin `CREATEDB`.
Tes mencakup commit, rollback, akses, versi, permintaan bersamaan, edit saat claim,
pembatalan, serta kegagalan pencatatan setelah commit. Kehilangan konfirmasi commit
akibat gangguan jaringan belum disimulasikan secara otomatis.

Endpoint dan integrasi session/frontend belum dihubungkan ke `Execute`.
