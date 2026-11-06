// Package config defines local startup and remote backend configuration.
// Secret values are inputs only and must never be logged.
package config

import (
	"errors"
	"github.com/fulcrumproject/utils/logging"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

const EnvPrefix = "S3_AGENT_"

type Config struct {
	AgentToken           string        `json:"agentToken" env:"FULCRUM_API_TOKEN" validate:"required"`
	FulcrumAPIURL        string        `json:"fulcrumApiUrl" env:"FULCRUM_API_URL" validate:"required"`
	JobPollInterval      time.Duration `json:"jobPollInterval" env:"JOB_POLL_INTERVAL"`
	MetricReportInterval time.Duration `json:"metricReportInterval" env:"METRIC_REPORT_INTERVAL"`
	HealthInterval       time.Duration `json:"healthInterval" env:"HEALTH_INTERVAL"`
	LogConfig            logging.Conf  `json:"log"`
}

func DefaultConfig() *Config {
	return &Config{
		FulcrumAPIURL:   "http://127.0.0.1:8080",
		JobPollInterval: 5 * time.Second, MetricReportInterval: 30 * time.Second,
		HealthInterval: 15 * time.Second,
		LogConfig:      logging.Conf{Level: slog.LevelInfo, Format: "json"},
	}
}

func (c Config) Validate() error {
	if c.AgentToken == "" {
		return errors.New("agent token is required")
	}
	if _, err := Endpoint(c.FulcrumAPIURL); err != nil {
		return errors.New("invalid core endpoint")
	}
	if c.JobPollInterval <= 0 || c.MetricReportInterval <= 0 || c.HealthInterval <= 0 {
		return errors.New("polling, metrics and health intervals must be positive")
	}
	return nil
}

type Backend struct {
	Endpoint                string `json:"endpoint"`
	ClientEndpoint          string `json:"clientEndpoint"`
	Region                  string `json:"region"`
	AccessKey               string `json:"accessKey"`
	SecretKey               string `json:"secretKey"`
	HTTPTimeoutSeconds      int    `json:"httpTimeoutSeconds"`
	OperationTimeoutSeconds int    `json:"operationTimeoutSeconds"`
}

func (c *Backend) Normalize() error {
	var err error
	if c.Endpoint, err = Endpoint(c.Endpoint); err != nil {
		return errors.New("invalid RustFS endpoint")
	}
	if c.ClientEndpoint, err = Endpoint(c.ClientEndpoint); err != nil {
		return errors.New("invalid customer endpoint")
	}
	if c.Endpoint == c.ClientEndpoint {
		return errors.New("customer endpoint must be separate from the private administrative endpoint")
	}
	if c.AccessKey == "" || c.SecretKey == "" || strings.HasPrefix(c.SecretKey, "vault://") {
		return errors.New("resolved administrative credentials are required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if c.HTTPTimeoutSeconds == 0 {
		c.HTTPTimeoutSeconds = 15
	}
	if c.OperationTimeoutSeconds == 0 {
		c.OperationTimeoutSeconds = 180
	}
	maxSeconds := int64((1<<63 - 1) / int64(time.Second))
	if c.HTTPTimeoutSeconds < 1 || c.OperationTimeoutSeconds < c.HTTPTimeoutSeconds ||
		int64(c.OperationTimeoutSeconds) > maxSeconds {
		return errors.New("invalid backend timeouts")
	}
	return nil
}

func Endpoint(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") ||
		(u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") {
		return "", errors.New("endpoint must be an HTTP(S) origin without userinfo, path, query or fragment")
	}
	return strings.TrimSuffix(value, "/"), nil
}
