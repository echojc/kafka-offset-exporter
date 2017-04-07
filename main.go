package main

import (
	"flag"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Shopify/sarama"
	log "github.com/Sirupsen/logrus"
)

func main() {
	brokerString := flag.String("brokers", "", "Kafka brokers to connect to, comma-separated")
	topics := flag.String("topics", "", "Only fetch offsets for topics matching this regex (default all)")

	port := flag.Int("port", 9000, "Port to export metrics on")
	path := flag.String("path", "/", "Path to export metrics on")

	refreshInterval := flag.Duration("refresh", 1*time.Minute, "Time between refreshing cluster metadata")
	fetchOffsetMinInterval := flag.Duration("fetchMin", 15*time.Second, "Min time before updating topic offsets")
	fetchOffsetMaxInterval := flag.Duration("fetchMax", 40*time.Second, "Max time before updating topic offsets")

	level := flag.String("level", "info", "Logger level")

	flag.Parse()

	logLevel, err := log.ParseLevel(*level)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(logLevel)
	log.SetFormatter(&log.JSONFormatter{})

	topicsFilter, err := regexp.Compile(*topics)
	if err != nil {
		log.Fatal(err)
	}

	kafka := mustNewKafka(*brokerString)
	defer kafka.Close()

	enforceGracefulShutdown(func(wg *sync.WaitGroup, shutdown chan struct{}) {
		startKafkaScraper(wg, shutdown, kafka, scrapeConfig{
			MetadataRefreshInterval: *refreshInterval,
			FetchOffsetMinInterval:  *fetchOffsetMinInterval,
			FetchOffsetMaxInterval:  *fetchOffsetMaxInterval,
			TopicsFilter:            topicsFilter,
		})
		startMetricsServer(wg, shutdown, serverConfig{
			port: *port,
			path: *path,
		})
	})
}

func enforceGracefulShutdown(f func(wg *sync.WaitGroup, shutdown chan struct{})) {
	wg := &sync.WaitGroup{}
	shutdown := make(chan struct{})
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-signals
		close(shutdown)
	}()

	log.Info("Graceful shutdown enabled")
	f(wg, shutdown)

	<-shutdown
	wg.Wait()
}

func mustNewKafka(brokerString string) sarama.Client {
	brokers := strings.Split(brokerString, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
		if !strings.ContainsRune(brokers[i], ':') {
			brokers[i] += ":9092"
		}
	}
	log.WithField("brokers.bootstrap", brokers).Info("connecting to cluster with bootstrap hosts")

	client, err := sarama.NewClient(brokers, nil)
	if err != nil {
		log.Fatal(err)
	}

	var addrs []string
	for _, b := range client.Brokers() {
		addrs = append(addrs, b.Addr())
	}
	log.WithField("brokers", addrs).Info("connected to cluster")

	return client
}
