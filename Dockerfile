FROM node:22-alpine AS frontend
WORKDIR /app/serve/frontend
COPY serve/frontend/package*.json ./
RUN npm ci
COPY serve/frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/serve/frontend/dist serve/frontend/dist
RUN CGO_ENABLED=0 go build -o /vega ./cmd/vega

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /vega /usr/local/bin/vega
VOLUME ["/config", "/data"]
EXPOSE 3001
ENTRYPOINT ["vega", "serve"]
CMD ["--addr", ":3001", "--db", "/data/vega.db"]
