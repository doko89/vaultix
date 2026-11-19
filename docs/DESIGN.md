# Design System (DESIGN)
## Vaultix — Centralized Infrastructure Backup Platform

**Status:** Draft
**Companion to:** [`PRD.md`](../PRD.md) (sumber kebutuhan produk) dan [`TECH_SPEC.md`](TECH_SPEC.md) (stack frontend §1.2, §5)
**Scope:** Manager Web UI — React + TypeScript, Tailwind CSS + shadcn/ui. Dokumen ini adalah acuan visual/interaksi; semua kebutuhan fungsional tetap mengacu ke PRD.

> Aturan baca: setiap bagian di bawah mencantumkan pasal PRD yang dirujuk. Jika ada konflik, PRD menang untuk perilaku, dokumen ini menang untuk tampilan.

---

## 1. Brand & Warna

**Primary color: red.** Merah dipakai untuk aksi utama, status kritis/gagal, dan aksen brand. Warna sukses/peringatan/info mengikuti konvensi operasional agar status backup langsung terbaca (ref. PRD §7.10, §7.15).

| Token | Nilai | Pakai untuk |
|---|---|---|
| `--primary` | `#DC2626` (red-600) | Tombol utama, link aktif, nav aktif, fokus ring |
| `--primary-hover` | `#B91C1C` (red-700) | Hover/press tombol utama |
| `--primary-fg` | `#FFFFFF` | Teks di atas primary |
| `--destructive` | `#DC2626` | Aksi destruktif, badge Failed/Offline, banner error |
| `--success` | `#16A34A` (green-600) | Badge Succeeded/Online/Healthy, tren positif |
| `--warning` | `#D97706` (amber-600) | Badge Running/Verifying/Uploading, retensi berisiko |
| `--info` | `#2563EB` (blue-600) | Badge Queued/Assigned, info netral |
| `--muted` | `#6B7280` (gray-500) | Badge Cancelled, teks sekunder |
| `--background` / `--surface` | `#FFFFFF` / `#F9FAFB` | Latar halaman / kartu |
| `--border` | `#E5E7EB` (gray-200) | Garis kartu, tabel, pembatas |

Catatan: shadcn/ui memakai token yang sama (`primary`, `destructive`, `muted`, ...), jadi tabel ini dipetakan 1:1 ke `web/src/shared/ui` (ref. TECH_SPEC §5). Jangan menambah warna aksen kedua tanpa persetujuan.

## 2. Tipografi & Bentuk

- Font: system stack (`Inter` bila dibundel, fallback `-apple-system, "Segoe UI", Roboto, sans-serif`).
- Skala: h1 24px/semibold (judul halaman), h2 18px/semibold (judul kartu/section), body 14px, caption/mono 12px.
- Radius: `soft` — tombol/input 6px, kartu 8px, badge pill.
- Data teknis (ID job, digest image, path, cron) selalu monospace.

## 3. Layout Global

- Shell: sidebar kiri (navigasi per seksi PRD §8) + topbar (pencarian global, status konektivitas Manager, menu user + tombol logout) + area konten.
- Semua halaman list (Agents, Policies, Jobs, Snapshots, Audit Logs) memakai pola sama: toolbar (search + filter + aksi utama) → tabel → pagination. Filter & search wajib ada (ref. PRD §7.15).
- Aksi utama tiap halaman memakai tombol primary merah, maksimal satu per layar; aksi destruktif memakai dialog konfirmasi eksplisit (ref. PRD §7.13 restore, §7.14 retensi).

## 4. Peta Halaman ← PRD §8

Setiap seksi PRD §8 adalah satu feature/route (ref. TECH_SPEC §1.2). Kolom PRD menunjukkan pasal perilaku yang wajib dipenuhi halaman tersebut.

| Route | Halaman | Acuan PRD |
|---|---|---|
| `/` | Dashboard: jumlah agen, online/offline, job per status, backup terakhir per agen/policy, tren durasi & volume, policy tanpa backup sukses | §7.15, §15 |
| `/agents` | Registrasi (token enrollment), tabel agen (hostname, IP, OS/arch, versi, heartbeat, kapasitas disk), revoke/disable, flag versi usang | §7.2 |
| `/agents/:id/resources` | Resource Discovery: path host, container/volume Docker, metadata Compose, label | §7.3 |
| `/policies` | CRUD policy: nama, target agen, tipe target (8 tipe §7.4), resource terpilih, metode, jadwal+timezone, retensi, kompresi, enkripsi (default mati), destinasi, enabled/disabled; tampilkan versi policy per job | §7.4–§7.8, §7.11 |
| `/schedules` | Jadwal one-time/recurring (hourly, daily, weekly), pause/resume, batas konkurensi, eksekusi manual | §7.9 |
| `/jobs` | Tabel job + detail: 8 state (§7.10), progress, byte read/compress/upload, rasio kompresi, status enkripsi + key ref, destinasi, snapshot/manifest ID, error & retry; aksi retry/cancel yang aman | §7.10, §7.11 |
| `/catalog` | Snapshot per agen/policy/target/tanggal; detail manifest; label jelas "unencrypted" bila tanpa enkripsi; hanya tampilkan restorable bila lolos integrity check | §7.11, §7.13 |
| `/restore` | Wizard restore (file/dir, database, volume, stack) + pilihan destinasi alternatif; konfirmasi eksplisit + peringatan overwrite; status job restore | §7.13 |
| `/storage` | Konfigurasi S3 (endpoint, region, bucket, prefix), tombol test koneksi, pilihan destinasi per policy | §7.12 |
| `/credentials` | Secret profile DB/service: form tanpa pernah menampilkan plaintext; hanya referensi + metadata | §7.7, §10.2 |
| `/users` | User lokal, role RBAC (§5.1–5.3), link/unlink identitas OIDC, group-to-role mapping | §5, §7.1.1 |
| `/notifications` | Destinasi email/webhook untuk job gagal & perubahan state agen | §7.16 |
| `/audit-logs` | Tabel actor, action, target, timestamp, outcome; tanpa secret/konten backup | §7.16, §10.2 |
| `/settings` | Konfigurasi global, provider OIDC (discovery/test/enable), health komponen | §7.1.1, §10.5 |
| `/login` | Login lokal + tombol SSO per provider OIDC aktif; error callback yang actionable tanpa membocorkan token/secret | §7.1.1, §8 |

## 5. Pembedaan Visual Tipe Target (ref. PRD §8, §7.4)

UI wajib membedakan lima kelompok ini lewat ikon + label, bukan warna saja:

1. Host filesystem path — ikon folder.
2. Metadata container Docker — ikon container.
3. Volume/bind mount Docker — ikon database/disk.
4. Target database (PostgreSQL, MySQL/MariaDB, SQL Server, Redis) — ikon database + badge nama engine.
5. Application stack — ikon layers/stack.

## 6. Status & State (ref. PRD §7.10)

Delapan state job dipetakan ke badge: `Queued` (info), `Assigned` (info), `Running` (warning, pulse), `Uploading` (warning, pulse + progress bar), `Verifying` (warning), `Succeeded` (success), `Failed` (destructive + pesan error actionable, ref. PRD §13.14), `Cancelled` (muted). Status agen: `Online` (success) / `Offline` (destructive) / `Stale` (warning, heartbeat > ambang). Snapshot yang belum lolos verifikasi tidak boleh memakai badge restorable (ref. PRD §7.13).

## 7. Keamanan di UI (ref. PRD §7.1, §10.2, §13.15)

- Sembunyikan aksi/tombol yang tidak diizinkan role user (RBAC §5), bukan sekadar disable — cek otorisasi tetap di server.
- Secret, kredensial, client secret OIDC, dan token tidak pernah dirender plaintext; field secret memakai input password + tombol reveal yang diaudit.
- Label enkripsi wajib di catalog & detail job: `Encrypted · key <ref>` atau `Unencrypted` (ref. PRD §7.11).
- Restore & hapus retensi selalu lewat dialog konfirmasi dengan nama target yang diketik/dicentang eksplisit.

## 8. Aksesibilitas & Responsif

- Kontras teks ≥ 4.5:1 untuk body; fokus keyboard terlihat (ring primary); semua ikon status punya teks.
- Layout responsif: sidebar jadi drawer di <1024px; tabel besar scroll horizontal dengan kolom aksi sticky; halaman kritis (login, dashboard, job detail) harus utuh di 390px.

## 9. Motion & Animasi

Prinsip: motion memberi umpan balik status, bukan dekorasi. Semua durasi di bawah adalah token (`--motion-*`); jangan hardcode durasi di komponen.

| Token | Nilai | Pakai untuk |
|---|---|---|
| `--motion-instant` | `100ms` | Hover tombol, fokus ring, toggle kecil |
| `--motion-fast` | `150ms ease-out` | Badge state, tooltip, dropdown, tab |
| `--motion-base` | `200ms ease-out` | Dialog, drawer sidebar, toast masuk/keluar |
| `--motion-slow` | `300ms ease-in-out` | Wizard restore antar-langkah, expand row tabel |
| `--motion-pulse` | `2s ease-in-out infinite` | Badge `Running`/`Uploading`, dot agen `Online` |

Aturan pakai:

- **Status aktif berdenyut, status final diam** (ref. PRD §7.10, §6 DESIGN): hanya `Running` dan `Uploading` yang pulse; `Succeeded`/`Failed`/`Cancelled` statis agar mudah dipindai.
- **Progress determinat, bukan spinner abadi**: job dengan byte terukur memakai progress bar; spinner hanya untuk operasi tanpa ukuran (test koneksi storage §7.12, test provider OIDC §7.1.1).
- **Loading list memakai skeleton** dengan shimmer halus, bukan layout kosong; baris tabel yang di-refresh via polling (ref. PRD §7.15, react-query di TECH_SPEC §5) di-update in-place tanpa menggeser scroll atau me-reset filter.
- **Perubahan state penting berkedip sekali**: baris job yang baru `Failed` atau agen yang baru `Offline` mendapat flash latar merah 1x (`--motion-slow`), lalu diam — cukup untuk menarik mata tanpa mengganggu.
- **Dialog konfirmasi destruktif** (restore, hapus retensi — ref. PRD §7.13, §7.14) masuk dengan fade+scale ringan; tombol konfirmasi tidak bergeser posisi selama dialog terbuka.
- **Toast**: notifikasi job gagal (ref. PRD §7.16) slide-in dari bawah-kanan, bertahan 6 detik, bisa di-dismiss; maksimal 3 toast menumpuk.
- **`prefers-reduced-motion`**: semua pulse, shimmer, dan transisi non-esensial dimatikan; flash 1x diganti border statis. Wajib lolos cek ini sebelum rilis.

## 10. Halaman Auth (ref. PRD §7.1, §7.1.1, §8)

Satu route `/login` melayani tiga konfigurasi; tampilannya beradaptasi, bukan tiga halaman berbeda.

**Layout**: kartu tengah (maks 400px) di atas latar netral; logo + nama "Vaultix" di atas kartu; judul "Masuk ke Vaultix"; footer kecil berisi tautan dokumentasi. Tidak ada sidebar/topbar — halaman auth berdiri sendiri. Harus utuh di 390px (ref. §8 DESIGN).

**Varian tampilan**:

1. **Lokal + OIDC** (default): tombol SSO per provider aktif di atas ("Masuk dengan <Provider>"), pemisah "atau", lalu form username + password + tombol primary merah "Masuk".
2. **Lokal saja**: hanya form username + password.
3. **OIDC saja** (login lokal dimatikan eksplisit): hanya tombol-tombol SSO; kartu menampilkan catatan "Login lokal dinonaktifkan administrator" — akun break-glass tetap ada di server (ref. PRD §7.1) tapi tidak diiklankan di UI.

**State & interaksi**:

- Tombol SSO per provider memakai nama + ikon resmi provider (bukan ikon generik); provider yang sedang gagal discovery tampil disabled dengan tooltip alasannya.
- Submit login lokal: tombol masuk state loading (spinner + "Memeriksa…", `--motion-instant`), form terkunci sampai respons kembali.
- Error callback OIDC (akses ditolak, kode kedaluwarsa, state tidak valid): banner merah di dalam kartu dengan pesan actionable ("Sesi login kedaluwarsa. Silakan coba lagi.") + tombol "Coba lagi" — tanpa token, claim, atau detail teknis (ref. PRD §7.1.1, §8).
- Error kredensial lokal: pesan generik "Username atau password salah" di bawah form (jangan bocorkan mana yang salah); tetap catat audit di server.
- User OIDC tanpa role termapping: layar penolakan jelas ("Akun Anda tidak memiliki akses. Hubungi administrator.") — bukan error teknis (ref. PRD §7.1.1, §13.18).
- Transisi: kartu fade-in sekali saat load (`--motion-base`); pindah antar state error memakai `--motion-fast` tanpa menggeser posisi tombol utama.

---

**End of document.**
