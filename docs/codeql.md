# Bonus Milestone 2 — SAST dengan CodeQL

Scan menggunakan [workflow CodeQL](.github/workflows/codeql.yml) di GitHub Actions.

## Bukti scan

Ambil branch, SHA, dan tautan run dari **Actions → CodeQL**. Isi jumlah temuan per bahasa dari **Code scanning** setelah job terkait berhasil.

| Bahasa | Branch dan SHA yang dipindai | Tautan run | Cakupan yang dikonfirmasi | Hasil dan bukti |
| --- | --- | --- | --- | --- |
| Go | `<branch> @ <SHA>` | `<link run>` | `<paket atau folder yang dipindai>` | Belum dijalankan |
| JavaScript/TypeScript | `<branch> @ <SHA>` | `<link run>` | `<file atau folder yang dipindai>` | Belum dijalankan |

Format kolom terakhir setelah scan berhasil bisa kayak  `0 temuan — <link atau screenshot>` atau `<jumlah> temuan — <link Code Scanning>`.

## Tinjauan temuan

Isi satu baris per alert. Tulis alasan teknis untuk keputusan **Perbaiki**, **False positive**, atau **Risiko diterima**. Gunakan `—` di kolom commit dan scan ulang jika temuan tidak diperbaiki.

| Rule / tautan alert | Severity | Lokasi kode | Potensi dampak | Keputusan dan alasan | Commit perbaikan | Bukti scan ulang |
| --- | --- | --- | --- | --- | --- | --- |
| `<rule ID> + <link alert>` | `<severity di GitHub>` | `<path/file:baris>` | `<dampak jika temuan valid>` | `<keputusan dan alasan>` | `<SHA perbaikan>` | `<link run dan hasil alert>` |

Kalau kedua job berhasil (tidak ada alert), ganti tabel temuan dengan `Tidak ada temuan`. 
