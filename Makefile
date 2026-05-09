.PHONY: build frontend-build serve-dev test clean types types-verify

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

# Remove build artifacts.
clean:
	rm -rf bin/
	rm -rf serve/frontend/dist
	rm -rf serve/frontend/node_modules
