.PHONY: run build docker-up docker-down db-clear tenant-setup tenant-reset test tidy

MANIFEST ?= tenants.local.json

run:
	go run cmd/api/main.go

build:
	go build -o bin/candidate-api cmd/api/main.go

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

# Local only: deletes every row in every public table; restart the API to reseed departments.
db-clear:
	@set -a; . ./.env; set +a; \
	[ "$$APP_ENV" = development ] || { echo "Refusing: APP_ENV must be development (got '$$APP_ENV')."; exit 1; }; \
	case "$$DB_HOST" in localhost|127.0.0.1) ;; *) echo "Refusing: DB_HOST must be localhost (got '$$DB_HOST')."; exit 1;; esac; \
	printf "Delete every row in database '%s'? Type its name to confirm: " "$$DB_NAME"; read answer; \
	[ "$$answer" = "$$DB_NAME" ] || { echo "Aborted."; exit 1; }; \
	docker-compose exec -T -e PGPASSWORD="$$DB_PASSWORD" postgres psql -v ON_ERROR_STOP=1 -U "$$DB_USER" -d "$$DB_NAME" -c \
	"DO \$$\$$ DECLARE tables text; BEGIN \
	SELECT string_agg(format('%I.%I', schemaname, tablename), ', ') INTO tables FROM pg_tables WHERE schemaname = 'public'; \
	IF tables IS NOT NULL THEN EXECUTE 'TRUNCATE ' || tables || ' RESTART IDENTITY CASCADE'; END IF; END \$$\$$;" && \
	echo "Cleared all tables in '$$DB_NAME'. Restart the API to reseed departments."

# Stop `make run` first. Usage: make tenant-setup [MANIFEST=tenants.local.json]
tenant-setup:
	@set -a; . ./.env; set +a; go run ./cmd/api -tenant-setup "$(MANIFEST)"

# Stop `make run` first. Usage: make tenant-reset TENANT=<tenant UUID>
tenant-reset:
	@[ -n "$(TENANT)" ] || { echo "Usage: make tenant-reset TENANT=<tenant UUID>"; exit 1; }
	@set -a; . ./.env; set +a; go run ./cmd/api -tenant-reset "$(TENANT)"

test:
	go test ./...

tidy:
	go mod tidy
