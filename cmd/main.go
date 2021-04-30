package main

import (
	"context"
	"fmt"
	"github.com/newrelic/infrastructure-agent/pkg/log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"


	"github.com/newrelic/newrelic-pixie-integration/internal/adapter"
	"github.com/newrelic/newrelic-pixie-integration/internal/config"
	"github.com/newrelic/newrelic-pixie-integration/internal/exporter"
	"github.com/newrelic/newrelic-pixie-integration/internal/worker"
	"px.dev/pxapi"
)

const (
	defaultRetries   = 100
	defaultSleepTime = 10 * time.Second
)

func main() {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	log.Info("Pixie integration is running...")
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	exporter, err := setExporterConnection(ctx, cfg.Exporter(), defaultRetries, defaultSleepTime)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	log.Debugf("Setting up Pixie client with cluster-id %s\n", cfg.Pixie().ClusterID())
	vz, err := setupPixie(ctx, cfg.Pixie(), defaultRetries, defaultSleepTime)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	var wg sync.WaitGroup
	runWorkers(ctx, cfg.Worker(), vz, exporter, &wg)
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		cancel()
	}()
	wg.Wait()
}

func runWorkers(ctx context.Context, cfg config.Worker, vz *pxapi.VizierClient, exporter exporter.Exporter, wg *sync.WaitGroup) {
	w := worker.Build(ctx, cfg, vz, exporter)
	go w.Spans(adapter.HTTPSpans(cfg.ClusterName()), wg)
	go w.Spans(adapter.MySQL(cfg.ClusterName()), wg)
	go w.Spans(adapter.PgSQL(cfg.ClusterName()), wg)
	go w.Metrics(adapter.HTTPMetrics(cfg.ClusterName()), wg)
	go w.Metrics(adapter.JVM(cfg.ClusterName()), wg)
	wg.Add(5)
}

func setExporterConnection(ctx context.Context, cfg config.Exporter, tries int, sleepTime time.Duration) (exp exporter.Exporter, err error) {
	log.Debug("Setting up OTLP exporter")
	for tries > 0 {
		exp, err = exporter.New(ctx, cfg)
		if err == nil {
			log.Infof("sending data to %s",cfg.Endpoint())
			return
		}
		tries -= 1
		log.Warning(err)
		time.Sleep(sleepTime)
	}
	return
}

func setupPixie(ctx context.Context, cfg config.Pixie, tries int, sleepTime time.Duration) (vz *pxapi.VizierClient, err error) {
	var client *pxapi.Client
	for tries > 0 {
		client, err = pxapi.NewClient(ctx, pxapi.WithAPIKey(cfg.APIKey()),pxapi.WithCloudAddr(cfg.Host()))
		if err == nil {
			vz, err = client.NewVizierClient(ctx, cfg.ClusterID())
			if err == nil {
				log.Infof("fetching data from cluster %s on %s",cfg.ClusterID(),cfg.Host())
				return
			}
			err = fmt.Errorf("error creating Pixie Vizier client: %w", err)
		} else {
			err = fmt.Errorf("error creating Pixie API client: %w", err)
		}
		tries -= 1
		log.Warning(err)
		time.Sleep(sleepTime)
	}
	return
}
