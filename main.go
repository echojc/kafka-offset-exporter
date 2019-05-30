package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Shopify/sarama"
	log "github.com/sirupsen/logrus"
)

func main() {
	brokerString := flag.String("brokers", "", "Kafka brokers to connect to, comma-separated")
	topics := flag.String("topics", "", "Only fetch offsets for topics matching this regex (default all)")
	groups := flag.String("groups", "", "Also fetch offsets for consumer groups matching this regex (default none)")
	port := flag.Int("port", 9000, "Port to export metrics on")
	path := flag.String("path", "/", "Path to export metrics on")
	refresh := flag.Duration("refresh", 1*time.Minute, "Time between refreshing cluster metadata")
	fetchMin := flag.Duration("fetchMin", 15*time.Second, "Min time before requesting updates from broker")
	fetchMax := flag.Duration("fetchMax", 40*time.Second, "Max time before requesting updates from broker")
	level := flag.String("level", "info", "Logger level")
	flag.Parse()

	mustSetupLogger(*level)
	serverConfig := mustNewServerConfig(*port, *path)
	scrapeConfig := mustNewScrapeConfig(*refresh, *fetchMin, *fetchMax, *topics, *groups)

	kafka := mustNewKafka(*brokerString)
	defer kafka.Close()

	enforceGracefulShutdown(func(wg *sync.WaitGroup, shutdown chan struct{}) {
		startKafkaScraper(wg, shutdown, kafka, scrapeConfig)
		startMetricsServer(wg, shutdown, serverConfig)
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

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V1_0_0_0
	client, err := sarama.NewClient(brokers, cfg)
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

func mustSetupLogger(level string) {
	logLevel, err := log.ParseLevel(level)
	if err != nil {
		log.Fatal(err)
	}

	log.SetLevel(logLevel)
	log.SetFormatter(&log.JSONFormatter{})
}
