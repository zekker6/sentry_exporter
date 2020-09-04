FROM golang:1.15 as builder

WORKDIR /app

ADD go.mod .
ADD go.sum .
RUN go mod download

ADD . .
RUN GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o sentry_exporter
RUN chmod +x sentry_exporter

FROM debian:buster-slim

RUN apt-get update && apt-get install -y ca-certificates

COPY --from=builder /app/sentry_exporter  /sentry_exporter
COPY sentry_exporter.yml       /config.yml

EXPOSE      9412
ENTRYPOINT  [ "/sentry_exporter" ]
CMD         [ "-config.file=/config.yml" ]
