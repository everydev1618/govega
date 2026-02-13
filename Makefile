.PHONY: build frontend-build serve-dev test clean

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

# Remove build artifacts.
clean:
	rm -rf bin/
	rm -rf serve/frontend/dist
	rm -rf serve/frontend/node_modules
