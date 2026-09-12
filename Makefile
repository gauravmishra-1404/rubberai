# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Gaurav Mishra
.PHONY: build test check
build:
	cd web && npm ci && npm run check && npm run build
	mkdir -p bin
	go build -buildvcs=false -trimpath -o bin/rubberai ./cmd/server
	go build -buildvcs=false -trimpath -o bin/rubberai-collector ./cmd/collector
test:
	go test -race ./...
	cd sdk/python && python3 -m unittest -v
check:
	go vet ./...
	cd web && npm run check
