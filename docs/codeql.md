# Bonus Milestone 2 — SAST dengan CodeQL

Scan menggunakan [workflow CodeQL](.github/workflows/codeql.yml) di GitHub Actions.

## Cakupan yang dikonfigurasi
| Bahasa | Cakupan | Proses analisis |
| --- | --- | --- |
| Go | Paket dalam modul Go, termasuk server, worker, dan paket internal yang dibangun oleh `go build ./...` | CodeQL menganalisis hasil ekstraksi build dengan versi Go dari `go.mod` dan CGO aktif. |
| JavaScript/TypeScript | Sumber JavaScript repo, termasuk komponen React di `frontend/src` | CodeQL memakai ekstraksi statis tanpa build Vite. |

## Bukti scan

Ambil branch, SHA, dan tautan run dari **Actions → CodeQL**. Isi jumlah temuan per bahasa dari **Code scanning** setelah job terkait berhasil.

| Bahasa | Branch dan SHA yang dipindai | Tautan run | Cakupan yang dikonfirmasi | Hasil dan bukti |
| --- | --- | --- | --- | --- |
| Go | `<branch> @ <SHA>` | `<link run>` | `<paket atau folder yang dipindai>` | Belum dijalankan |
| Go | PR #17 (`feat/codeql` → `develop`) @ `19f9c9b9ce2f7323e3a489367db85e07f34c42f2` | [CodeQL run — PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45879295393) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) |
| Go | PR #18 (`k6` → `develop`) @ `07031d44ef1d38f9016ec6f526caa48cd730df76` | [CodeQL run — PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45927260207) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | `<branch> @ <SHA>` | `<link run>` | `<file atau folder yang dipindai>` | Belum dijalankan |
| JavaScript/TypeScript | PR #17 (`feat/codeql` → `develop`) @ `19f9c9b9ce2f7323e3a489367db85e07f34c42f2` | [CodeQL run — PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45879295393) | 	Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | PR #18 (`k6` → `develop`) @ `07031d44ef1d38f9016ec6f526caa48cd730df76` | [CodeQL run — PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45927260207) | Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) |

Format kolom terakhir setelah scan berhasil bisa kayak  `0 temuan — <link atau screenshot>` atau `<jumlah> temuan — <link Code Scanning>`.

## Tinjauan temuan

Isi satu baris per alert. Tulis alasan teknis untuk keputusan **Perbaiki**, **False positive**, atau **Risiko diterima**. Gunakan `—` di kolom commit dan scan ulang jika temuan tidak diperbaiki.

| Rule / tautan alert | Severity | Lokasi kode | Potensi dampak | Keputusan dan alasan | Commit perbaikan | Bukti scan ulang |
| --- | --- | --- | --- | --- | --- | --- |
| `<rule ID> + <link alert>` | `<severity di GitHub>` | `<path/file:baris>` | `<dampak jika temuan valid>` | `<keputusan dan alasan>` | `<SHA perbaikan>` | `<link run dan hasil alert>` |
| [Hasil Code Scanning PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#17 | - | - | 
| [Hasil Code Scanning PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#18 | - | - | 

Kalau kedua job berhasil (tidak ada alert), ganti tabel temuan dengan `Tidak ada temuan`. 
