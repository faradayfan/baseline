# Multi-stage build for the Baseline service. Produces a small, non-root,
# distroless image. Buildx-friendly: pass --platform linux/arm64 to target the
# Raspberry Pi cluster (the Go toolchain cross-compiles; TARGETOS/TARGETARCH are
# provided by buildx).
#
#   docker buildx build --platform linux/arm64 -t <registry>/baseline:<tag> --push .

# --- build stage ---
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off → a fully static binary that runs on distroless/static.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/baseline ./cmd/baseline

# --- runtime stage ---
# distroless static, nonroot (uid 65532) — matches the cluster convention.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/baseline /usr/local/bin/baseline

# HTTP by default on :8080. The reaper (BASELINE_REAP) and MCP-HTTP
# (BASELINE_MCP_HTTP) modes are selected by env at runtime.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/baseline"]
