# Deploy Pantara Backend ke Dokploy

## 1. Sumber Git & Build
- **Repository:** `https://github.com/Orang2-BU/PANTARA-BACK`
- **Branch:** `main`
- **Build Type:** `Dockerfile`
- **Build Path:** `/`
- **Docker File:** `Dockerfile`
- **Exposed / Internal Port:** `8000`
- **Healthcheck Path:** `/healthz` (readiness DB check: `/readyz`)

## 2. Database & Redis (Dokploy Services / Same Network)
1. Buat service **PostgreSQL 16** dan **Redis 7** di project Dokploy yang sama.
2. Backend otomatis menjalankan migrasi SQL (`001_init.sql` dan `002_corporate_first.sql` — aman dan idempotent) saat container start (`store.Migrate`), dengan retry koneksi saat cold-start.
3. Untuk **production bersih**, set `SEED_DEMO=false` (admin pertama daftar dari halaman `/daftar` di frontend). Untuk **pilot / demo** agar dataset Arunika Digital + akun `admin`, `manajer`, `hr@pantara.id`, dan `bima@pantara.id` langsung terisi saat DB kosong, set `SEED_DEMO=true` atau jalankan `/app/pantara-seed` satu kali di container.

## 3. Environment Variables (Dokploy)
```env
APP_ENV=production
PORT=8000
# Set true khusus environment demo/pilot agar dataset + akun HR/Manajer/Admin/Anggota langsung tersedia
SEED_DEMO=false

# Ganti host/user/password/db sesuai service Postgres Dokploy
DATABASE_URL=postgres://pantara:CHANGE_THIS_PASSWORD@pantara-postgres:5432/pantara_db?sslmode=disable
DB_MAX_CONNS=80
DB_MIN_CONNS=15

# Disarankan di production untuk rate-limit terdistribusi & cache AI lintas instance (aman untuk 1000 concurrent users)
REDIS_URL=redis://pantara-redis:6379/0

# WAJIB diganti string acak >= 32 karakter (contoh: openssl rand -hex 32)
# Aplikasi menolak start jika APP_ENV=production masih memakai dev secret bawaan.
JWT_SECRET=CHANGE_TO_RANDOM_HEX_MINIMUM_32_CHARS
COOKIE_SECURE=auto
COOKIE_DOMAIN=

# Domain HTTPS frontend yang mengakses API
CORS_ALLOWED_ORIGINS=https://pantara.domainmu.com
APP_BASE_URL=https://pantara.domainmu.com

# AI Task DNA (kosongkan jika belum ada API key; otomatis pakai deterministic 5-sample engine)
AI_PROVIDER_URL=
AI_API_KEY=
AI_MODEL=gpt-4o-mini
AI_TIMEOUT_SECONDS=15

# SMTP Email Undangan & Reset Sandi (opsional; kosong = log ke stdout container)
SMTP_HOST=
SMTP_PORT=587
SMTP_USER=
SMTP_PASS=
SMTP_FROM=noreply@domainmu.com
```

## 4. Koneksi ke Service Frontend
Pastikan service frontend (`pantara-front`) di project Dokploy yang sama memakai **Internal Container/Service Name** backend ini pada env:
```env
NEXT_PUBLIC_DATA_MODE=api
BACKEND_INTERNAL_URL=http://<nama-service-backend-dokploy>:8000
```
