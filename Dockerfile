FROM golang:1.12 as builder
RUN go get -d github.com/prune998/kafka-offset-exporter
WORKDIR /go/src/github.com/prune998/kafka-offset-exporter
RUN CGO_ENABLED=0 GOOS=linux go build

FROM alpine:3.9
RUN apk --no-cache add ca-certificates && \
    adduser -D kafka
WORKDIR /home/kafka
USER kafka

COPY --from=0 /go/src/github.com/prune998/kafka-offset-exporter/kafka-offset-exporter  .
EXPOSE 9000
ENTRYPOINT ["/home/kafka/kafka-offset-exporter"]
