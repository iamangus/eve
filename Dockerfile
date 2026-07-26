# Stage 1: build the frontend
FROM node:20-alpine AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# Stage 2: build the Go binary (embeds frontend/dist)
FROM golang:1.25-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eve ./cmd/server/

# Stage 3: runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S eve && adduser -S -G eve eve
WORKDIR /app
COPY --from=backend /out/eve /app/eve
USER eve
ENV LISTEN=:8090
ENV DB_PATH=/data/data.db
VOLUME ["/data"]
EXPOSE 8090
ENTRYPOINT ["/app/eve"]