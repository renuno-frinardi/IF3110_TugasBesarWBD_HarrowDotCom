# Bonus Milestone 2 — SAST dengan CodeQL

Scan menggunakan [workflow CodeQL](.github/workflows/codeql.yml) di GitHub Actions.

## Cakupan yang dikonfigurasi
| Bahasa | Cakupan | Proses analisis |
| --- | --- | --- |
| Go | Paket dalam modul Go, termasuk server, worker, dan paket internal yang dibangun oleh `go build ./...` | CodeQL menganalisis hasil ekstraksi build dengan versi Go dari `go.mod` dan CGO aktif. |
| JavaScript/TypeScript | Sumber JavaScript repo, termasuk komponen React di `frontend/src` | CodeQL memakai ekstraksi statis tanpa build Vite. |

## Bukti scan

| Bahasa | Branch dan SHA yang dipindai | Tautan run | Cakupan yang dikonfirmasi | Hasil dan bukti |
| --- | --- | --- | --- | --- |
| Go | PR #17 (`feat/codeql` → `develop`) @ `19f9c9b9ce2f7323e3a489367db85e07f34c42f2` | [CodeQL run — PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45879295393) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) |
| Go | PR #18 (`k6` → `develop`) @ `07031d44ef1d38f9016ec6f526caa48cd730df76` | [CodeQL run — PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45927260207) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) |
| Go | PR #19 (`style/frontend` → `develop`) @ `de59609d48bb07b889fc112c1cdb8146d7a1c99a` | [CodeQL run — PR #19](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45935809481) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #19](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A19+tool%3ACodeQL+is%3Aopen) |
| Go | PR #20 (`refactor/split-fe` → `develop`) @ `40593eb8b763cbf91c97c101c035ca60cfa17993` | [CodeQL run — PR #20](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45947379703) | ./... dari root modul Go | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #20](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A20+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | PR #17 (`feat/codeql` → `develop`) @ `19f9c9b9ce2f7323e3a489367db85e07f34c42f2` | [CodeQL run — PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45879295393) | 	Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | PR #18 (`k6` → `develop`) @ `07031d44ef1d38f9016ec6f526caa48cd730df76` | [CodeQL run — PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45927260207) | Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | PR #19 (`style/frontend` → `develop`) @ `de59609d48bb07b889fc112c1cdb8146d7a1c99a` | [CodeQL run — PR #19](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45935809481) | Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #19](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A19+tool%3ACodeQL+is%3Aopen) |
| JavaScript/TypeScript | PR #20 (`refactor/split-fe` → `develop`) @ `40593eb8b763cbf91c97c101c035ca60cfa17993` | [CodeQL run — PR #20](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/actions/runs/45947379703) | Kode JS/TS dalam repo, termasuk frontend/src/ | Upload berhasil dan hasil selesai diproses. [0 alert terbuka pada PR #20](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A20+tool%3ACodeQL+is%3Aopen) |


## Tinjauan temuan

| Rule / tautan alert | Severity | Lokasi kode | Potensi dampak | Keputusan dan alasan | Commit perbaikan | Bukti scan ulang |
| --- | --- | --- | --- | --- | --- | --- |
| [Hasil Code Scanning PR #17](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A17+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#17 | - | - | 
| [Hasil Code Scanning PR #18](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A18+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#18 | - | - | 
| [Hasil Code Scanning PR #19](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A19+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#19 | - | - | 
| [Hasil Code Scanning PR #20](https://github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/security/code-scanning?query=pr%3A19+tool%3ACodeQL+is%3Aopen) | - | - | - | Tidak ada alert CodeQL pada PR#20 | - | - | 
