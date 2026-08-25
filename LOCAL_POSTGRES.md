# Local Postgres — Quick Start

> Decisions locked: **UUID v4**, **local Postgres for build/test**, then **Neon**; audit in PG TTL 30d; hospital MD/receptionist migrated.

## 1. Start Postgres (requires Docker Desktop / Podman)

```powershell
# from ambigo-backend/
docker compose up -d postgres
docker compose ps
# check logs
docker compose logs -f postgres
```

Default DSN (already in `config/config.go` fallback + `.env.example`):
```
DATABASE_URL=postgres://ambigo:ambigo_dev_password@localhost:5432/ambigo?sslmode=disable
```

## 2. Run migrations (goose)

```powershell
# install goose CLI once
go install github.com/pressly/goose/v3/cmd/goose@latest

# from ambigo-backend/
$env:DATABASE_URL="postgres://ambigo:ambigo_dev_password@localhost:5432/ambigo?sslmode=disable"
goose -dir migrations postgres $env:DATABASE_URL up
goose -dir migrations postgres $env:DATABASE_URL status
```

Or via Go (embedded, like production will):
```go
// in cmd/server/main.go after pool init:
//go:embed migrations/*.sql
// var embedMigrations embed.FS
// goose.SetBaseFS(embedMigrations)
// goose.UpContext(ctx, sqlDB, ".")
```

## 3. Build & run backend

```powershell
go mod tidy
go build ./...
go run ./cmd/server
# health
curl http://localhost:8080/api/v1/health
```

## 4. When ready for Neon

- Create Neon project → copy `DATABASE_URL` (sslmode=require)
- Set env `DATABASE_URL` in Render / VPS `EnvironmentFile`
- `goose -dir migrations postgres $DATABASE_URL up`
- No code change — `pgxpool` handles pgcrypto/gen_random_uuid on Neon.

## 5. Useful SQL

```sql
-- verify
SELECT table_name FROM information_schema.tables WHERE table_schema='public' ORDER BY table_name;
\d rides
\d hospitals

-- TTL cleanup (run via pg_cron or Go ticker)
DELETE FROM auth_otp WHERE created_at < now() - interval '5 minutes';
DELETE FROM audit_log WHERE created_at < now() - interval '30 days';
```

## 6. Notes

- **IDs are UUID v4** (`gen_random_uuid()` default). API still returns `_id` as string — no mobile app change.
- **Money types are NUMERIC(10,2)** — not FLOAT.
- **H3 cells** are `TEXT[]` with GIN index; query via `WHERE h3_cells && ARRAY['cell']` or `WHERE 'cell' = ANY(h3_cells)`.
- Without Docker, you can run Postgres via `scoop install postgresql` or WSL2 — same DSN.
