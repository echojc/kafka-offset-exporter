package main

import (
	"errors"
	"math/rand"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	log "github.com/Sirupsen/logrus"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type scrapeConfig struct {
	FetchMinInterval        time.Duration
	FetchMaxInterval        time.Duration
	MetadataRefreshInterval time.Duration
	TopicsFilter            *regexp.Regexp
	GroupsFilter            *regexp.Regexp
}

func mustNewScrapeConfig(refresh time.Duration, fetchMin time.Duration, fetchMax time.Duration, topics string, groups string) scrapeConfig {
	topicsFilter, err := regexp.Compile(topics)
	if err != nil {
		log.Fatal(err)
	}

	// empty group should match nothing instead of everything
	if groups == "" {
		groups = ".^"
	}
	groupsFilter, err := regexp.Compile(groups)
	if err != nil {
		log.Fatal(err)
	}

	return scrapeConfig{
		FetchMinInterval:        fetchMin,
		FetchMaxInterval:        fetchMax,
		MetadataRefreshInterval: refresh,
		TopicsFilter:            topicsFilter,
		GroupsFilter:            groupsFilter,
	}
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

func manageBroker(wg *sync.WaitGroup, shutdown chan struct{}, broker *sarama.Broker, kafka sarama.Client, cfg scrapeConfig) {
	wg.Add(1)
	defer wg.Done()

	log.WithField("broker", broker.Addr()).
		WithField("interval.min", cfg.FetchMinInterval.String()).
		WithField("interval.max", cfg.FetchMaxInterval.String()).
		Info("Starting handler for broker")

	wait := time.After(0)
	for {
		select {
		case <-wait:
			log.WithField("broker", broker.Addr()).Debug("Updating metrics")

			// ensure broker is connected
			if err := connect(broker); err != nil {
				log.WithField("broker", broker.Addr()).
					WithField("error", err).
					Error("Failed to connect to broker")
				break
			}

			// fetch groups coordinated by this broker
			var groups []string
			groupsResponse, err := broker.ListGroups(&sarama.ListGroupsRequest{})
			if err != nil {
				log.WithField("broker", broker.Addr()).
					WithField("error", err).
					Error("Failed to retrieve consumer groups")
			} else if groupsResponse.Err != sarama.ErrNoError {
				log.WithField("broker", broker.Addr()).
					WithField("error", groupsResponse.Err).
					Error("Failed to retrieve consumer groups")
			} else {
				for group := range groupsResponse.Groups {
					if !cfg.GroupsFilter.MatchString(group) {
						log.WithField("group", group).Info("not found group")
						continue
					}

					groupCoordinator, err := kafka.Coordinator(group)
					if err != nil {
						log.WithField("broker", broker.Addr()).
							WithField("group", group).
							WithField("error", err).
							Warn("Failed to identify broker for consumer group")
						continue
					}

					if broker == groupCoordinator {
						groups = append(groups, group)
					}
				}
			}
			if len(groups) == 0 {
				log.WithField("broker", broker.Addr()).Debug("No consumer groups to fetch offsets for")
			}

			// build requests
			partitionCount := 0
			oldestRequest := sarama.OffsetRequest{}
			newestRequest := sarama.OffsetRequest{}
			var groupRequests []sarama.OffsetFetchRequest
			for _, group := range groups {
				groupRequests = append(groupRequests, sarama.OffsetFetchRequest{ConsumerGroup: group, Version: 1})
			}

			// fetch partitions led by this broker, and add all partitions to group requests
			topics, err := kafka.Topics()
			if err != nil {
				log.WithField("broker", broker.Addr()).
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
					log.WithField("broker", broker.Addr()).
						WithField("topic", topic).
						WithField("error", err).
						Warn("Failed to get partitions for topic")
					continue
				}

				for _, partition := range partitions {
					// all partitions need to be added to group requests
					for i := range groupRequests {
						groupRequests[i].AddPartition(topic, partition)
					}

					partitionLeader, err := kafka.Leader(topic, partition)
					if err != nil {
						log.WithField("broker", broker.Addr()).
							WithField("topic", topic).
							WithField("partition", partition).
							WithField("error", err).
							Warn("Failed to identify broker for partition")
						continue
					}

					if broker == partitionLeader {
						oldestRequest.AddBlock(topic, partition, sarama.OffsetOldest, 1)
						newestRequest.AddBlock(topic, partition, sarama.OffsetNewest, 1)
						partitionCount++
					}
				}
			}

			if partitionCount == 0 {
				log.WithField("broker", broker.Addr()).Debug("No partitions for broker to fetch")
			}

			log.WithField("broker", broker.Addr()).
				WithField("partition.count", partitionCount).
				WithField("group.count", len(groupRequests)).
				Debug("Sending requests")

			requestWG := &sync.WaitGroup{}
			requestWG.Add(2 + len(groupRequests))
			go func() {
				defer requestWG.Done()
				handleTopicOffsetRequest(broker, &oldestRequest, "oldest", metricOffsetOldest)
			}()
			go func() {
				defer requestWG.Done()
				handleTopicOffsetRequest(broker, &newestRequest, "newest", metricOffsetNewest)
			}()
			for i := range groupRequests {
				go func(request *sarama.OffsetFetchRequest) {
					defer requestWG.Done()
					handleGroupOffsetRequest(broker, request, metricOffsetConsumer)
				}(&groupRequests[i])
			}
			requestWG.Wait()

		case <-shutdown:
			log.WithField("broker", broker.Addr()).Info("Shutting down handler for broker")
			return
		}

		min := int64(cfg.FetchMinInterval)
		max := int64(cfg.FetchMaxInterval)
		duration := time.Duration(rand.Int63n(max-min) + min)

		wait = time.After(duration)
		log.WithField("broker", broker.Addr()).
			WithField("interval.rand", duration.String()).
			Debug("Updated metrics and waiting for next run")
	}
}

func connect(broker *sarama.Broker) error {
	if ok, _ := broker.Connected(); ok {
		return nil
	}

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_10_0_0
	if err := broker.Open(cfg); err != nil {
		return err
	}

	if connected, err := broker.Connected(); err != nil {
		return err
	} else if !connected {
		return errors.New("Unknown failure")
	}

	return nil
}

func handleGroupOffsetRequest(broker *sarama.Broker, request *sarama.OffsetFetchRequest, metric *prometheus.GaugeVec) {
	response, err := broker.FetchOffset(request)
	if err != nil {
		log.WithField("broker", broker.Addr()).
			WithField("group", request.ConsumerGroup).
			WithField("error", err).
			Error("Failed to request group offsets")
		return
	}

	for topic, partitions := range response.Blocks {
		for partition, block := range partitions {
			if block == nil {
				log.WithField("broker", broker.Addr()).
					WithField("group", request.ConsumerGroup).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Failed to get data for group")
				continue
			} else if block.Err != sarama.ErrNoError {
				log.WithField("broker", broker.Addr()).
					WithField("group", request.ConsumerGroup).
					WithField("topic", topic).
					WithField("partition", partition).
					WithField("error", block.Err).
					Warn("Failed getting data for group")
				continue
			} else if block.Offset < 0 {
				continue
			}

			metric.With(prometheus.Labels{
				"topic":     topic,
				"partition": strconv.Itoa(int(partition)),
				"group":     request.ConsumerGroup,
			}).Set(float64(block.Offset))
		}
	}
}

func handleTopicOffsetRequest(broker *sarama.Broker, request *sarama.OffsetRequest, offsetType string, metric *prometheus.GaugeVec) {
	response, err := broker.GetAvailableOffsets(request)
	if err != nil {
		log.WithField("broker", broker.Addr()).
			WithField("offset.type", offsetType).
			WithField("error", err).
			Error("Failed to request topic offsets")
		return
	}

	for topic, partitions := range response.Blocks {
		for partition, block := range partitions {
			if block == nil {
				log.WithField("broker", broker.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Failed to get data for partition")
				continue
			} else if block.Err != sarama.ErrNoError {
				log.WithField("broker", broker.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					WithField("error", block.Err).
					Warn("Failed getting offsets for partition")
				continue
			} else if len(block.Offsets) != 1 {
				log.WithField("broker", broker.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Got unexpected offset data for partition")
				continue
			}

			metric.With(prometheus.Labels{
				"topic":     topic,
				"partition": strconv.Itoa(int(partition)),
			}).Set(float64(block.Offsets[0]))
		}
	}
}
