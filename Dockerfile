FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o raffle ./cmd/raffle

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/raffle /usr/local/bin/raffle
EXPOSE 8543
CMD ["raffle"]