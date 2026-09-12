# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Gaurav Mishra
FROM node:22-alpine AS dashboard
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run check && npm run build

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=dashboard /src/internal/platform/web/ ./internal/platform/web/
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /rubberai ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 rubberai
COPY --from=backend /rubberai /usr/local/bin/rubberai
USER rubberai
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["rubberai"]
