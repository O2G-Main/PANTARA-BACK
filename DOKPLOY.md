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
2. Backend otomatis menjalankan migrasi SQL saat container start (`store.Migrate`), dengan retry koneksi saat cold-start.
3. Di mode produksi (`APP_ENV=production`), seed demo otomatis **mati** supaya database bersih. User pertama cukup daftar di halaman `/daftar` frontend (atau jalankan manual `/app/pantara-seed` bila butuh data demo).

## 3. Environment Variables (Dokploy)
```env
APP_ENV=production
PORT=8000
SEED_DEMO=false

# Ganti host/user/password/db sesuai service Postgres Dokploy
DATABASE_URL=postgres://pantara:CHANGE_THIS_PASSWORD@pantara-postgres:5432/pantara_db?sslmode=disable
DB_MAX_CONNS=80
DB_MIN_CONNS=15

# Opsional namun disarankan di production untuk rate-limit & cache AI lintas instance
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
BACKEND_INTERNAL_URL=http://<nama-service-backend-dokploy>:8000
```
