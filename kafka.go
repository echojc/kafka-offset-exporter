package main

import (
	"math/rand"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	log "github.com/Sirupsen/logrus"
	"github.com/prometheus/client_golang/prometheus"
)

type scrapeConfig struct {
	FetchOffsetMinInterval  time.Duration
	FetchOffsetMaxInterval  time.Duration
	MetadataRefreshInterval time.Duration
	TopicsFilter            *regexp.Regexp
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func startKafkaScraper(wg *sync.WaitGroup, shutdown chan struct{}, kafka sarama.Client, cfg scrapeConfig) {
	go refreshMetadataPeriodically(wg, shutdown, kafka, cfg)
	for _, broker := range kafka.Brokers() {
		go manageBroker(wg, shutdown, broker, kafka, cfg)
	}
}

func refreshMetadataPeriodically(wg *sync.WaitGroup, shutdown chan struct{}, kafka sarama.Client, cfg scrapeConfig) {
	wg.Add(1)
	defer wg.Done()

	log.WithField("interval", cfg.MetadataRefreshInterval.String()).
		Info("Starting metadata refresh thread")

	wait := time.After(0)
	for {
		select {
		case <-wait:
			log.Debug("Refreshing cluster metadata")
			if err := kafka.RefreshMetadata(); err != nil {
				log.WithField("error", err).Warn("Failed to update cluster metadata")
			}
		case <-shutdown:
			log.Info("Shutting down metadata refresh thread")
			return
		}
		wait = time.After(cfg.MetadataRefreshInterval)
	}
}

func manageBroker(wg *sync.WaitGroup, shutdown chan struct{}, me *sarama.Broker, kafka sarama.Client, cfg scrapeConfig) {
	wg.Add(1)
	defer wg.Done()

	log.WithField("broker", me.Addr()).
		WithField("interval.min", cfg.FetchOffsetMinInterval.String()).
		WithField("interval.max", cfg.FetchOffsetMaxInterval.String()).
		Info("Starting handler for broker")

	wait := time.After(0)
	for {
		select {
		case <-wait:
			log.WithField("broker", me.Addr()).Debug("Updating metrics")
			var topicPartitions = make(map[string][]int32)

			// accessing Kafka has concurrent overhead, so we extract all relevant information first
			topics, err := kafka.Topics()
			if err != nil {
				log.WithField("broker", me.Addr()).
					WithField("error", err).
					Error("Failed to get topics")
				break
			}

			for _, topic := range topics {
				if !cfg.TopicsFilter.MatchString(topic) {
					continue
				}

				partitions, err := kafka.Partitions(topic)
				if err != nil {
					log.WithField("broker", me.Addr()).
						WithField("topic", topic).
						WithField("error", err).
						Warn("Failed to get partitions for topic")
					continue
				}

				for _, partition := range partitions {
					broker, err := kafka.Leader(topic, partition)
					if err != nil {
						log.WithField("broker", me.Addr()).
							WithField("topic", topic).
							WithField("partition", partition).
							WithField("error", err).
							Warn("Failed to identify broker for partition")
						continue
					}

					if broker == me {
						topicPartitions[topic] = append(topicPartitions[topic], partition)
					}
				}
			}

			if len(topicPartitions) == 0 {
				log.WithField("broker", me.Addr()).
					Debug("No partitions for broker to fetch")
				continue
			}

			// request data for each offset type
			queries := []struct {
				name   string
				offset int64
				metric *prometheus.GaugeVec
			}{
				{"oldest", sarama.OffsetOldest, metricOffsetOldest},
				{"newest", sarama.OffsetNewest, metricOffsetNewest},
			}
			for _, query := range queries {
				partitionCount := 0
				request := sarama.OffsetRequest{}
				for topic, partitions := range topicPartitions {
					for _, partition := range partitions {
						request.AddBlock(topic, partition, query.offset, 1)
						partitionCount++
					}
				}
				log.WithField("broker", me.Addr()).
					WithField("topic.count", len(topicPartitions)).
					WithField("partition.count", partitionCount).
					WithField("offset.type", query.name).
					Debug("Sending offset request")

				response, err := me.GetAvailableOffsets(&request)
				if err != nil {
					log.WithField("broker", me.Addr()).
						WithField("offset.type", query.name).
						WithField("error", err).
						Error("Failed to request offsets")
					break
				}
				log.WithField("broker", me.Addr()).
					WithField("offset.type", query.name).
					Debug("Received offset response")

				for topic, partitions := range topicPartitions {
					for _, partition := range partitions {
						block := response.GetBlock(topic, partition)
						if block == nil {
							log.WithField("broker", me.Addr()).
								WithField("topic", topic).
								WithField("partition", partition).
								Warn("Failed to get data for partition")
							continue
						} else if block.Err != sarama.ErrNoError {
							log.WithField("broker", me.Addr()).
								WithField("topic", topic).
								WithField("partition", partition).
								WithField("error", block.Err).
								Warn("Failed getting offsets for partition")
							continue
						} else if len(block.Offsets) != 1 {
							log.WithField("broker", me.Addr()).
								WithField("topic", topic).
								WithField("partition", partition).
								Warn("Got unexpected offset data for partition")
							continue
						}

						query.metric.With(prometheus.Labels{
							"topic":     topic,
							"partition": strconv.Itoa(int(partition)),
							"leader":    me.Addr(),
						}).Set(float64(block.Offsets[0]))
					}
				}
			}

		case <-shutdown:
			log.WithField("broker", me.Addr()).Info("Shutting down handler for broker")
			return
		}

		min := int64(cfg.FetchOffsetMinInterval)
		max := int64(cfg.FetchOffsetMaxInterval)
		duration := time.Duration(rand.Int63n(max-min) + min)

		wait = time.After(duration)
		log.WithField("broker", me.Addr()).
			WithField("interval.rand", duration.String()).
			Debug("Waiting to update metrics")
	}
}
