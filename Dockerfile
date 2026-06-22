# syntax=docker/dockerfile:1

# --------------------------------------------------------------------------- #
# Build stage
# --------------------------------------------------------------------------- #
# go.mod requires go 1.26.2, so we need golang:1.26 (or newer).
# golang:1.23 (as suggested in task brief draft) is too old and will fail.
FROM golang:1.26 AS build

WORKDIR /src

# Download dependencies first (layer cache friendly).
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY . .

# TARGET selects which cmd/ binary to build: worker | orchestrator
ARG TARGET=worker

RUN CGO_ENABLED=0 go build -o /out/app ./cmd/${TARGET}

# --------------------------------------------------------------------------- #
# Final stage — minimal distroless image
# --------------------------------------------------------------------------- #
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=build /out/app /app/app

# Config files and seed data must be present at runtime.
COPY configs/ /app/configs/
COPY data/    /app/data/

ENTRYPOINT ["/app/app"]
