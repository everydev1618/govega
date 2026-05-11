.PHONY: build frontend-build serve-dev test test-pg clean types types-verify \
        pg-up pg-down pg-reset pg-shell pg-url

# Build the vega binary with embedded frontend.
build: frontend-build
	go build -o bin/vega ./cmd/vega

# Build only the frontend.
frontend-build:
	@if [ -f serve/frontend/package.json ]; then \
		cd serve/frontend && npm install && npm run build; \
	else \
		mkdir -p serve/frontend/dist && touch serve/frontend/dist/.gitkeep; \
	fi

# Start the Go server and Vite dev server for development.
serve-dev:
	@echo "Start Go server:  go run ./cmd/vega serve <file.vega.yaml>"
	@echo "Start Vite dev:   cd serve/frontend && npm run dev"

# Run all Go tests.
test:
	go test ./...

# Regenerate the @govega/api-types TypeScript package from docs/openapi.yaml.
# Commit api.ts after running.
types:
	cd types && npm install --silent && npm run generate

# Verify the committed types/api.ts matches a fresh generation.
# Fails (non-zero exit) if the OpenAPI spec has changed without a corresponding
# regen + commit. Wire into CI to prevent drift.
types-verify:
	cd types && npm install --silent && npm run verify

# --- Local Postgres for dual-backend dev / testing (refs #61) ---

# Connection URL the dev Postgres exposes. localhost:5433 is intentional
# — keeps this out of the way of a host Postgres on 5432.
DEV_PG_URL ?= postgres://vega:vega@localhost:5433/vega_test?sslmode=disable

# Spin up Postgres in the background and create the test database.
# Re-running is idempotent — the container restarts if already up and
# the CREATE DATABASE is guarded.
pg-up:
	docker compose -f dev/docker-compose.yml up -d
	@echo "Waiting for Postgres to accept connections..."
	@for i in $$(seq 1 30); do \
	  if docker compose -f dev/docker-compose.yml exec -T postgres pg_isready -U vega -d vega_dev >/dev/null 2>&1; then \
	    break; \
	  fi; \
	  sleep 1; \
	done
	@docker compose -f dev/docker-compose.yml exec -T postgres \
	  psql -U vega -d vega_dev -tAc "SELECT 1 FROM pg_database WHERE datname='vega_test'" | grep -q 1 \
	  || docker compose -f dev/docker-compose.yml exec -T postgres createdb -U vega vega_test
	@echo "Postgres ready at $(DEV_PG_URL)"

# Stop and remove the container (keeps the named volume so data
# persists across restarts). Use 'docker volume rm govega_pgdata' to
# nuke storage too.
pg-down:
	docker compose -f dev/docker-compose.yml down

# Drop and recreate vega_test so the next test run starts from an
# empty schema. Cheap — about 1 second.
pg-reset:
	@docker compose -f dev/docker-compose.yml exec -T postgres dropdb --if-exists -U vega vega_test
	@docker compose -f dev/docker-compose.yml exec -T postgres createdb -U vega vega_test
	@echo "vega_test recreated"

# Open an interactive psql session against the test database. Use to
# poke around after a test run if something's off.
pg-shell:
	docker compose -f dev/docker-compose.yml exec postgres psql -U vega -d vega_test

# Print the URL so other tools can pick it up (eg. \`eval $(make pg-url)\`).
pg-url:
	@echo "export VEGA_TEST_POSTGRES_URL='$(DEV_PG_URL)'"

# Run the test suite against both SQLite and the dev Postgres. Brings
# Postgres up if it isn't already, but doesn't reset between runs —
# call \`make pg-reset\` first if you want a clean slate.
test-pg: pg-up
	VEGA_TEST_POSTGRES_URL='$(DEV_PG_URL)' go test ./...

# Remove build artifacts.
clean:
	rm -rf bin/
	rm -rf serve/frontend/dist
	rm -rf serve/frontend/node_modules
