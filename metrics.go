package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
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

func init() {
	prometheus.MustRegister(metricOffsetOldest)
	prometheus.MustRegister(metricOffsetNewest)
	prometheus.MustRegister(metricOffsetConsumer)
}

func startMetricsServer(wg *sync.WaitGroup, shutdown chan struct{}, cfg serverConfig) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		mux := http.NewServeMux()

		// healthz basic
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			m := map[string]interface{}{"version": version, "status": "OK"}

			b, err := json.Marshal(m)
			if err != nil {
				http.Error(w, "cant marshal healthz json", http.StatusInternalServerError)
				return
			}

			w.Write(b)
		})

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
