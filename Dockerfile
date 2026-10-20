# syntax=docker/dockerfile:1

FROM golang:1.26 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /bin/app ./cmd/bot

FROM gcr.io/distroless/base-debian12
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
COPY --from=builder /bin/app /app
ENTRYPOINT ["/app"]
