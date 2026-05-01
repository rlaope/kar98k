FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /kar ./cmd/kar98k

FROM alpine:3.19
COPY --from=builder /kar /usr/local/bin/kar
ENTRYPOINT ["kar"]
