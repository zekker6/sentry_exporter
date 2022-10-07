FROM golang:1.18@sha256:97d5ff988c41993a716890463f2cf8c18fcf0ce694dfd9b963fb831170cd9212 as builder

WORKDIR /app

ADD go.mod .
ADD go.sum .
RUN go mod download

ADD . .
RUN GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o sentry_exporter
RUN chmod +x sentry_exporter

FROM debian:buster-slim@sha256:557ee531b81ce380d012d83b7bb56211572e5d6088d3e21a3caef7d7ed7f718b

RUN apt-get update && apt-get install -y ca-certificates

COPY --from=builder /app/sentry_exporter  /sentry_exporter
COPY sentry_exporter.yml       /sentry_exporter.yml

EXPOSE      9412
ENTRYPOINT  [ "/sentry_exporter" ]
CMD         [ "-config.file=/sentry_exporter.yml" ]
