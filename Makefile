.PHONY: run build docker-up docker-down db-clear tenant-setup tenant-reset e2e test tidy

MANIFEST ?= tenants.local.json
ENV_FILE ?= .env

run:
	go run cmd/api/main.go

build:
	go build -o bin/candidate-api cmd/api/main.go

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

# Stop every API replica first. Deletes every row and every avatar, then asks you to type the DB name
# (and host when not local). Non-local hosts need CONFIRM=production and get a pg_dump in backups/ first.
# Usage: make db-clear [ENV_FILE=.env.production CONFIRM=production]
db-clear:
	@set -a; . "$(abspath $(ENV_FILE))"; set +a; \
	case "$$DB_HOST" in localhost|127.0.0.1) ;; *) \
		[ "$(CONFIRM)" = production ] || { echo "Refusing: DB_HOST '$$DB_HOST' is not local; rerun with CONFIRM=production."; exit 1; }; \
		mkdir -p backups; file="backups/$$DB_NAME-$$(date +%Y%m%d-%H%M%S).sql"; echo "Backing up to $$file"; \
		docker run --rm -e PGPASSWORD="$$DB_PASSWORD" -e PGSSLMODE="$$DB_SSL_MODE" postgres:18-alpine \
			pg_dump -h "$$DB_HOST" -p "$$DB_PORT" -U "$$DB_USER" -d "$$DB_NAME" > "$$file" \
			|| { rm -f "$$file"; echo "Backup failed; nothing deleted."; exit 1; };; \
	esac; \
	go run ./cmd/api -db-clear

# Stop `make run` first. Usage: make tenant-setup [ENV_FILE=.env] [MANIFEST=tenants.local.json]
tenant-setup:
	@set -a; . "$(abspath $(ENV_FILE))"; set +a; go run ./cmd/api -tenant-setup "$(MANIFEST)"

# Stop every API replica first. Deletes the tenant, its Admin, and all its data after you type the Admin email.
# Usage: make tenant-reset TENANT=<tenant UUID> [ENV_FILE=.env]
tenant-reset:
	@[ -n "$(TENANT)" ] || { echo "Usage: make tenant-reset TENANT=<tenant UUID>"; exit 1; }
	@set -a; . "$(abspath $(ENV_FILE))"; set +a; go run ./cmd/api -tenant-reset "$(TENANT)"

# Local only, destructive: builds and starts the API on APP_PORT, runs every candidate flow
# for the first two MANIFEST tenants, then resets the first. Stop `make run` first.
# Usage: make e2e [ENV_FILE=.env] [MANIFEST=tenants.local.json]
e2e:
	@set -a; . "$(abspath $(ENV_FILE))"; set +a; \
	E2E=1 E2E_MANIFEST="$(abspath $(MANIFEST))" go test ./cmd/api -run TestE2E -count=1 -v

test:
	go test ./...

tidy:
	go mod tidy
