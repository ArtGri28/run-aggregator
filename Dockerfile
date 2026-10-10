# syntax=docker/dockerfile:1
# Образ приложения: один статический бинарник /aggregator с подкомандами
# bot, worker, migrate, healthcheck (PRD-01, MVP.md §10.2).

# Сборка идёт на архитектуре сборщика и кросс-компилирует под целевую
# (Mac arm64 -> VPS amd64) без эмуляции.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Узкий COPY вместо «COPY . .»: в корне репозитория лежит .env с секретами,
# в слой образа он попасть не должен (образ уезжает на чужой хост).
# .dockerignore — вторая линия защиты. Миграции встроены в бинарник
# (migrations/embed.go), каталог нужен только для сборки. Статика Mini App
# (web/embed.go) встроена так же.
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/aggregator ./cmd/aggregator \
 && mkdir -p /out/data/raw

# distroless: нет оболочки и пакетного менеджера, есть ca-certificates, tzdata
# и пользователь nonroot (65532) — минимум поверхности для атаки.
# Из-за отсутствия оболочки healthcheck в compose задан только в exec-форме.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/aggregator /aggregator
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
WORKDIR /data
ENTRYPOINT ["/aggregator"]
