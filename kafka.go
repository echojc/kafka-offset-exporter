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

type scrapeConfig struct {
	FetchOffsetMinInterval  time.Duration
	FetchOffsetMaxInterval  time.Duration
	MetadataRefreshInterval time.Duration
	TopicsFilter            *regexp.Regexp
	GroupsFilter            *regexp.Regexp
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

			// ensure broker is connected
			if err := connect(me); err != nil {
				log.WithField("broker", me.Addr()).
					WithField("error", err).
					Error("Failed to connect to broker")
				break
			}

			// fetch groups coordinated by this broker
			var groups []string
			groupsResponse, err := me.ListGroups(&sarama.ListGroupsRequest{})
			if err != nil {
				log.WithField("broker", me.Addr()).
					WithField("error", err).
					Error("Failed to retrieve consumer groups")
			} else if groupsResponse.Err != sarama.ErrNoError {
				log.WithField("broker", me.Addr()).
					WithField("error", groupsResponse.Err).
					Error("Failed to retrieve consumer groups")
			}
			for group := range groupsResponse.Groups {
				if !cfg.GroupsFilter.MatchString(group) {
					continue
				}

				broker, err := kafka.Coordinator(group)
				if err != nil {
					log.WithField("broker", me.Addr()).
						WithField("group", group).
						WithField("error", err).
						Warn("Failed to identify broker for consumer group")
					continue
				}

				if broker == me {
					groups = append(groups, group)
				}
			}

			if len(groups) == 0 {
				log.WithField("broker", me.Addr()).
					Debug("No consumer groups to fetch offsets for")
				break
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
					// all partitions need to be added to group requests
					for i := range groupRequests {
						groupRequests[i].AddPartition(topic, partition)
					}

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
						oldestRequest.AddBlock(topic, partition, sarama.OffsetOldest, 1)
						newestRequest.AddBlock(topic, partition, sarama.OffsetNewest, 1)
						partitionCount++
					}
				}
			}

			if partitionCount == 0 {
				log.WithField("broker", me.Addr()).
					Debug("No partitions for broker to fetch")
				break
			}

			log.WithField("broker", me.Addr()).
				WithField("partition.count", partitionCount).
				WithField("group.count", len(groupRequests)).
				Debug("Sending requests")

			wg := &sync.WaitGroup{}
			wg.Add(2 + len(groupRequests))
			go func() {
				defer wg.Done()
				handleTopicOffsetRequest(me, &oldestRequest, "oldest", metricOffsetOldest)
			}()
			go func() {
				defer wg.Done()
				handleTopicOffsetRequest(me, &newestRequest, "newest", metricOffsetNewest)
			}()
			for i := range groupRequests {
				go func() {
					defer wg.Done()
					handleGroupOffsetRequest(me, &groupRequests[i], metricOffsetConsumer)
				}()
			}
			wg.Wait()

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
			Debug("Updated metrics and waiting for next run")
	}
}

func connect(b *sarama.Broker) error {
	if ok, _ := b.Connected(); ok {
		return nil
	}

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_10_0_0
	if err := b.Open(cfg); err != nil {
		return err
	}

	if connected, err := b.Connected(); err != nil {
		return err
	} else if !connected {
		return errors.New("Unknown failure")
	}

	return nil
}

func handleGroupOffsetRequest(b *sarama.Broker, request *sarama.OffsetFetchRequest, metric *prometheus.GaugeVec) {
	response, err := b.FetchOffset(request)
	if err != nil {
		log.WithField("broker", b.Addr()).
			WithField("group", request.ConsumerGroup).
			WithField("error", err).
			Error("Failed to request group offsets")
		return
	}

	for topic, partitions := range response.Blocks {
		for partition, block := range partitions {
			if block == nil {
				log.WithField("broker", b.Addr()).
					WithField("group", request.ConsumerGroup).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Failed to get data for group")
				continue
			} else if block.Err != sarama.ErrNoError {
				log.WithField("broker", b.Addr()).
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

func handleTopicOffsetRequest(b *sarama.Broker, request *sarama.OffsetRequest, offsetType string, metric *prometheus.GaugeVec) {
	response, err := b.GetAvailableOffsets(request)
	if err != nil {
		log.WithField("broker", b.Addr()).
			WithField("offset.type", offsetType).
			WithField("error", err).
			Error("Failed to request topic offsets")
		return
	}

	for topic, partitions := range response.Blocks {
		for partition, block := range partitions {
			if block == nil {
				log.WithField("broker", b.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Failed to get data for partition")
				continue
			} else if block.Err != sarama.ErrNoError {
				log.WithField("broker", b.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					WithField("error", block.Err).
					Warn("Failed getting offsets for partition")
				continue
			} else if len(block.Offsets) != 1 {
				log.WithField("broker", b.Addr()).
					WithField("topic", topic).
					WithField("partition", partition).
					Warn("Got unexpected offset data for partition")
				continue
			}

			metric.With(prometheus.Labels{
				"topic":     topic,
				"partition": strconv.Itoa(int(partition)),
				"leader":    b.Addr(),
			}).Set(float64(block.Offsets[0]))
		}
	}
}
