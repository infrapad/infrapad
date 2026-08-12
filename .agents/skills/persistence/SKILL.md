---
name: persistence
description: Work on persistence-related tasks including database schema, store interfaces, and Postgres implementation. Use when modifying database design, store layer, migrations, or any data persistence logic.
---

# Persistence

Before working on any persistence-related task (database schema, migrations, store interfaces, Postgres queries, data models), read the following files to understand the current design and implementation:

1. **Database design document** — read `docs/db-design.md` for the overall schema and design decisions.
2. **Store interface** — read `server/pkg/store/store.go` for the abstract store interface.
3. **Postgres implementation** — read all files in `server/pkg/store/postgres/` for the concrete implementation, including migrations in `server/pkg/store/postgres/migrations/`.

Always ensure changes are consistent across all three layers: design doc, store interface, and Postgres implementation.

## Migrations

Migrations use [goose](https://github.com/pressly/goose) and are embedded via `go:embed` in `server/pkg/store/postgres/store.go`. They run automatically on startup via `goose.Up`.

Migration files live in `server/pkg/store/postgres/migrations/`. The are created via 

Each migration file must contain `-- +goose Up` and `-- +goose Down` sections:

```sql
-- +goose Up
CREATE TABLE example (id UUID PRIMARY KEY);

-- +goose Down
DROP TABLE example;
```

When adding a new migration, use the next sequential number (e.g., if the last file is `00001_initial.sql`, create `00002_description.sql`).

### Task targets

The `server/Taskfile.yaml` provides `task` targets for managing migrations (run from the `server/` directory or prefix with `server:` from the root).
The main ones to use are:

| Command | Description |
|---|---|
| `task server:db:migrate:up` | Apply all pending migrations |
| `task server:db:migrate:down` | Roll back the last applied migration |
| `task server:db:migrate:redo` | Roll back and re-apply the last migration |
| `task server:db:migrate:create -- name` | Create a new migration file |

Use `task -l | grep 'server:db'` for more tools if needed.
