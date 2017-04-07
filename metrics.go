package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	metricOffsetOldest = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kafka_offset_oldest",
			Help: "Oldest offset for a partition",
		},
		[]string{
			"topic",
			"partition",
			"leader",
		},
	)
	metricOffsetNewest = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kafka_offset_newest",
			Help: "Newest offset for a partition",
		},
		[]string{
			"topic",
			"partition",
			"leader",
		},
	)
	metricOffsetConsumer = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kafka_offset_consumer",
			Help: "Current offset for a consumer group",
		},
		[]string{
			"topic",
			"partition",
			"group",
		},
	)
)

type serverConfig struct {
	port int
	path string
}

func init() {
	prometheus.MustRegister(metricOffsetOldest)
	prometheus.MustRegister(metricOffsetNewest)
	prometheus.MustRegister(metricOffsetConsumer)
}

func startMetricsServer(wg *sync.WaitGroup, shutdown chan struct{}, cfg serverConfig) {
	go func() {
		wg.Add(1)
		defer wg.Done()

		mux := http.NewServeMux()
		mux.Handle(cfg.path, promhttp.Handler())
		srv := &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.port),
			Handler: mux,
		}
		go func() {
			log.WithField("port", cfg.port).
				WithField("path", cfg.path).
				Info("Starting metrics HTTP server")
			srv.ListenAndServe()
		}()

		<-shutdown
		log.Info("Shutting down metrics HTTP server")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
}
