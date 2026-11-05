# syntax=docker/dockerfile:1.7
FROM golang:1.27.3-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" -o /out/bpm-api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bpm-worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /out/bpm-api /app/bpm-api

COPY --from=build --chown=nonroot:nonroot /out/bpm-worker /app/bpm-worker

USER nonroot:nonroot
EXPOSE 8080
CMD ["/app/bpm-api"]

