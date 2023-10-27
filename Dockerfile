FROM golang:1.21 as builder

ARG version="no_version"
ARG buildtime="12345"

COPY . /go/src/github.com/prune998/kafka-offset-exporter
WORKDIR /go/src/github.com/prune998/kafka-offset-exporter
RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags "-X main.version=$(version)-$(buildtime)"

FROM alpine:3
RUN apk --no-cache add ca-certificates && \
    adduser -D kafka
WORKDIR /home/kafka
USER kafka

COPY --from=0 /go/src/github.com/prune998/kafka-offset-exporter/kafka-offset-exporter  .
EXPOSE 9000
ENTRYPOINT ["/home/kafka/kafka-offset-exporter"]
