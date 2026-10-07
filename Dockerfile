ARG GO_IMAGE=golang:1.25-alpine

# Build the API as a static Linux binary.
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/candidate-api ./cmd/api

# Runtime image. Configure it with environment variables; no .env file is baked in.
FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S -G app app
WORKDIR /app
USER app

COPY --from=build --chown=app:app /out/candidate-api ./candidate-api

EXPOSE 8080
ENTRYPOINT ["/app/candidate-api"]
