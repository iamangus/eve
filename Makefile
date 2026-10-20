.PHONY: frontend build test e2e vet check audit

frontend:
	cd frontend && npm ci --ignore-scripts && npm run build

build: frontend
	go build -trimpath -tags goolm -o eve ./cmd/server/

test: frontend
	go test -tags goolm -race ./...
	cd frontend && npm test

vet: frontend
	go vet -tags goolm ./...

e2e:
	cd frontend && npm run test:e2e

audit: frontend
	cd frontend && npm audit
	go run golang.org/x/vuln/cmd/govulncheck@latest -tags goolm ./...

check: frontend test e2e vet audit
