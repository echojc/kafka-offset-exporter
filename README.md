# kafka-offset-exporter

This is a Prometheus exporter for topic and consumer group offsets in Kafka.
Your Kafka cluster must be on version 0.10.0.0 or above for this to work.

## Usage

The only required parameter is a set of brokers to bootstrap into the cluster.

By default, the oldest and newest offsets of all topics are retrieved and
reported.  You can also enable offset reporting for any consumer group but note
that due to the way Sarama works, this requires querying for offsets for _all_
partitions for each consumer group which can take a long time. It is recommended
to filter both topics and consumer groups to just the ones you care about.

```
$ ./kafka-offset-exporter -help
Usage of ./kafka-offset-exporter:
  -brokers string
        Kafka brokers to connect to, comma-separated
  -fetchMax duration
        Max time before requesting updates from broker (default 40s)
  -fetchMin duration
        Min time before requesting updates from broker (default 15s)
  -groups string
        Also fetch offsets for consumer groups matching this regex (default none)
  -level string
        Logger level (default "info")
  -path string
        Path to export metrics on (default "/")
  -port int
        Port to export metrics on (default 9000)
  -refresh duration
        Time between refreshing cluster metadata (default 1m0s)
  -topics string
        Only fetch offsets for topics matching this regex (default all)
```

## Dockerfile

First build the binary to include in the Docker container. Default this generates a 10M binary. With (optional) `-ldflags="-s -w"` it becomes 6.5M.
```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" .
```

Optional optimization: final result 1.7M binary.
```
upx --ultra-brute kafka-offset-exporter
```

Now build the docker image:
```
docker build -t kafka-offset-exporter .
```

Run the image:
```
docker run -d -p 9000:9000 kafka-offset-exporter
```

Additionally add arguments, e.g.:
```
docker run -d -p 9000:9000 kafka-offset-exporter -brokers 127.0.0.1:9092
```
