// Command agent starts the Fulcrum S3 control-plane agent.
// It reuses stdagent and never proxies customer object traffic.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fulcrumproject/agent-lib-go/pkg/fulcrumcli"
	"github.com/fulcrumproject/agent-lib-go/pkg/stdagent"
	"github.com/fulcrumproject/agent-s3/internal/config"
	"github.com/fulcrumproject/agent-s3/internal/rustfsapi"
	"github.com/fulcrumproject/agent-s3/internal/s3agent"
	"github.com/fulcrumproject/agent-s3/internal/safelog"
	"github.com/fulcrumproject/utils/confbuilder"
	"github.com/fulcrumproject/utils/logging"
)

func main() { os.Exit(run()) }
func run() int {
	path := flag.String("config", "", "Local configuration file")
	flag.Parse()
	cfg, err := confbuilder.New(config.DefaultConfig()).EnvPrefix(config.EnvPrefix).EnvFiles(".env").File(path).Build()
	if err != nil {
		slog.Error("Invalid local configuration (details suppressed)")
		return 1
	}
	slog.SetDefault(slog.New(safelog.New(logging.NewLogger(&cfg.LogConfig).Handler())))
	if err = cfg.Validate(); err != nil {
		slog.Error("Invalid local configuration", "error", err)
		return 1
	}
	core := fulcrumcli.NewHTTPClient(cfg.FulcrumAPIURL, cfg.AgentToken)
	factory := func(cfg config.Backend) (s3agent.Backend, error) { return rustfsapi.New(cfg) }
	a, err := s3agent.New(factory, core,
		stdagent.WithJobPollInterval(cfg.JobPollInterval),
		stdagent.WithHealthInterval(cfg.HealthInterval),
		stdagent.WithMetricsReportInterval(cfg.MetricReportInterval),
		stdagent.WithConnectRetryInterval(5*time.Second))
	if err != nil {
		slog.Error("Cannot initialize agent", "error", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err = a.Run(ctx); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		slog.Error("Agent startup failed", "error", err)
		return 1
	}
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if err = a.Shutdown(shutdown); err != nil {
		slog.Error("Agent shutdown failed", "error", err)
		return 1
	}
	return 0
}
