# kafka-offset-exporter

----

## Description

This is a Prometheus exporter for topic and consumer group offsets in Kafka.
Your Kafka cluster must be on version 0.10.0.0 or above for this to work.

----

## Changelog

Please refer to the `CHANGELOG` file

## Migration to Go Mod

As the origial version of this project is not maintained anymore, I decided to fork it and move it to `go mod`.
For a user point of view, there's nothing to do to use this project as usual.

## Build

Simply clone the repo and `go build`, or use `go get` :

```bash
go get github.com/prune998/kafka-offset-exporter
```

### Docker

There is a basic `Dockerfile` that will build and package the application. The final image is based on Alpine.

To build it yourself, just clone the repository and build it :

```bash
git clone https://github.com/prune998/kafka-offset-exporter
cd kafka-offset-exporter
docker build -t kafka-offset-exporter:latest .
docker run -ti --rm prune/kafka-offset-exporter:v1.0.5
```

There is a maintained image at `prune/kafka-offset-exporter:v1.0.5` :

```bash
docker pull prune/kafka-offset-exporter:v1.0.5
docker run -ti --rm prune/kafka-offset-exporter:v1.0.5
```

In both case, add your arguments on the commandline (see *Usage*):

```bash
docker run -ti --rm prune/kafka-offset-exporter:v1.0.5 -brokers localhost:9092
```

## Usage

The only required parameter is a set of brokers to bootstrap into the cluster.

Parameters can be set on the commandline (see below) or defined as *Environment Variables*, or both at the same time :

```bash
LEVEL=debug ./kafka-offset-exporter -brokers=localhost:9092
```

This is usefull when you're deploying in Kubernetes :

```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: kafka-offset-exporter
  labels:
    app: "kafka-offset-exporter"
    release: "v1.0.5"
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app: "kafka-offset-exporter"
        release: "v1.0.5"
    spec:
      containers:
      - name: kafka-offset-exporter
        image: "prune/kafka-offset-exporter:v1.0.5"
        imagePullPolicy: "Always"
        args:
        - "-brokers"
        - "localhost:9092"
        env:
        - name: LEVEL
          value: "debug"
```

By default, the oldest and newest offsets of all topics are retrieved and
reported.  You can also enable offset reporting for any consumer group but note
that due to the way Sarama works, this requires querying for offsets for _all_
partitions for each consumer group which can take a long time. It is recommended
to filter both topics and consumer groups to just the ones you care about.

```bash
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
        Logger level (default "warn")
  -endpoint string
        Path to export metrics on (default "/metrics")
  -port int
        Port to export metrics on (default 9000)
  -refresh duration
        Time between refreshing cluster metadata (default 1m0s)
  -topics string
        Only fetch offsets for topics matching this regex (default all)
```

### Scraping with Prometheus-Operator

Prometheus Operator deployed in Kubernetes use a `ServiceMonitor` Custom Resource to scrape Services (you can't scrape pods directly).

#### Service

Create the service in the same `Namespace` as the `kafka-offset-exporter` using `kubectl -n your_namespace apply -f kafka-offset-exporter-svc.yml`

```yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: kafka-offset-exporter
    release: kafka
  name: kafka-offset-exporter
spec:
  ports:
  - name: http
    port: 9000
    protocol: TCP
    targetPort: 9000
  selector:
    app: kafka-offset-exporter
  sessionAffinity: None
  type: ClusterIP
```

#### ServiceMonitor

Create the serviceMonitor in the same `Namespace` as `Prometheus` (in the `monitoring` namespace) using `kubectl -n monitoring -f kafka-offset-exporter-svcmon.yml`

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  labels:
    app: kafka-offset-exporter
    k8s-app: "true"
  name: kafka-offset-exporter
spec:
  endpoints:
  - interval: 5s
    port: http
  namespaceSelector:
    any: true
  selector:
    matchLabels:
      app: kafka-offset-exporter
```

## The Original of kafka-offset-exporter version no longer maintained

The original version of this project is located at <https://github.com/echojc/kafka-offset-exporter>
It is not maintained anymore, while this fork is. Please submit PRs here.

From the orignial author Jonathan Chow <https://github.com/echojc> :

```text
My employer used to rely heavily on Kafka and so I could dogfood and iterate on
this project regularly. Unfortunately, this is no longer the case and I don't
have the resources to maintain and/or develop this anymore.

I don't feel comfortable recommending people to use outdated and unmaintained
software, so please consider using an established fork, forking this yourself,
or creating a new-and-improved exporter as an alternative.

Thanks all!
```

----
