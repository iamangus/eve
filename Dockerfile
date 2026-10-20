# Stage 1: build the frontend
FROM node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402 AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# Stage 2: build the Go binary (embeds frontend/dist)
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -tags goolm -ldflags="-s -w" -o /out/eve ./cmd/server/

# Stage 3: runtime
FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S eve && adduser -S -G eve eve && \
    mkdir -p /data /app/data && chown -R eve:eve /data /app && chmod 700 /data /app/data
WORKDIR /app
COPY --from=backend /out/eve /app/eve
USER eve
ENV LISTEN=:8090
ENV DATA_DIR=/data
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8090/healthz || exit 1
EXPOSE 8090
ENTRYPOINT ["/app/eve"]
