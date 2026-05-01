FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o /echoserver .

FROM alpine:3.19
COPY --from=builder /echoserver /usr/local/bin/echoserver
EXPOSE 8080
ENTRYPOINT ["echoserver"]
