// Agent integrates the S3 handlers with the shared Fulcrum runtime.
// Polling, secret resolution, health scheduling and metric delivery remain in stdagent.
package s3agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/fulcrumproject/agent-lib-go/pkg/agent"
	"github.com/fulcrumproject/agent-lib-go/pkg/stdagent"
	"github.com/fulcrumproject/agent-s3/internal/config"
	"time"
)

type RemoteConfig struct {
	RustFS *config.Backend `json:"rustfs"`
}
type Factory func(config.Backend) (Backend, error)
type Agent struct {
	std     *stdagent.Agent
	factory Factory
	backend Backend
	config  config.Backend
	id      string
}

func New(factory Factory, core stdagent.FulcrumClient, opts ...stdagent.AgentOption) (*Agent, error) {
	if factory == nil || core == nil {
		return nil, errors.New("backend factory and core client are required")
	}
	std, err := stdagent.New(core, opts...)
	if err != nil {
		return nil, err
	}
	a := &Agent{std: std, factory: factory}
	if err = std.OnConnect(agent.ConnectHandlerWrapper(a.Connect)); err != nil {
		return nil, err
	}
	if err = std.OnHealth(a.Health); err != nil {
		return nil, err
	}
	for _, action := range []string{"create", "update", "suspend", "resume", "delete"} {
		if err = std.OnJob(action, agent.JobHandlerWrapper(a.Handle)); err != nil {
			return nil, err
		}
	}
	if err = std.OnMetrics(agent.MetricsReporterWrapper(a.Metrics)); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Agent) Connect(ctx context.Context, info *agent.AgentInfo[RemoteConfig]) error {
	if info == nil || !validID(info.ID) || info.Config == nil || info.Config.RustFS == nil {
		return errors.New("agent identity and remote RustFS configuration are required")
	}
	cfg := *info.Config.RustFS
	if err := cfg.Normalize(); err != nil {
		return err
	}
	backend, err := a.factory(cfg)
	if err != nil {
		return err
	}
	if backend == nil {
		return errors.New("backend factory returned no client")
	}
	if err := backend.Ping(ctx); err != nil {
		return err
	}
	a.id, a.config, a.backend = info.ID, cfg, backend
	return nil
}
func (a *Agent) Run(ctx context.Context) error      { return a.std.Run(ctx) }
func (a *Agent) Shutdown(ctx context.Context) error { return a.std.Shutdown(ctx) }
func (a *Agent) Health(ctx context.Context) error {
	if a.backend == nil {
		return errors.New("backend is not connected")
	}
	return a.backend.Ping(ctx)
}

func (a *Agent) Handle(ctx context.Context, job *agent.Job[Properties, Properties, Resources]) (response *agent.JobResponse[Resources], err error) {
	// Never let a panic value containing request data reach stdagent's panic logger.
	defer func() {
		if recover() != nil {
			response = nil
			err = errors.New("internal S3 handler failure")
		}
	}()
	if a.backend == nil {
		return nil, errors.New("backend is not connected")
	}
	if job == nil || job.Service == nil || job.Service.AgentID != a.id {
		return nil, errors.New("job service does not belong to this agent")
	}
	n, err := namesFor(job.Service.ID, a.id)
	if err != nil {
		return nil, err
	}
	if n.Key == a.config.AccessKey {
		return nil, ErrOwnership
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.config.OperationTimeoutSeconds)*time.Second)
	defer cancel()
	var quota uint64
	switch job.Action {
	case "create":
		props := job.Params
		if props == nil || props.QuotaBytes == 0 {
			props = &Properties{}
			if job.Service.Properties != nil {
				props.QuotaBytes = job.Service.Properties.QuotaBytes
			}
		}
		quota, err = a.create(ctx, n, job.Service.ID, props)
	case "update":
		if job.Params == nil {
			return nil, errors.New("update properties are required")
		}
		if job.Service.Status == "New" || job.Service.Status == "CreateFailed" {
			quota, err = a.create(ctx, n, job.Service.ID, job.Params)
		} else {
			quota, err = a.update(ctx, n, job.Service.ID, job.Params)
		}
	case "suspend", "resume":
		quota, err = a.setAccess(ctx, n, job.Service.ID, job.Action == "resume")
	case "delete":
		err = a.delete(ctx, n, job.Service.ID)
	default:
		err = errors.New("unsupported S3 action")
	}
	if err != nil {
		return nil, err
	}
	return &agent.JobResponse[Resources]{
		AgentInstanceID:   &n.Bucket,
		AgentInstanceData: &Resources{Bucket: n.Bucket, AccessKeyID: n.Key, Endpoint: a.config.ClientEndpoint, Region: a.config.Region, QuotaBytes: quota},
	}, nil
}

func (a *Agent) Metrics(ctx context.Context, svc *agent.Service[Properties, Resources]) ([]agent.MetricEntry, error) {
	if a.backend == nil {
		return nil, errors.New("backend is not connected")
	}
	if svc == nil || svc.AgentID != a.id {
		return nil, errors.New("invalid metrics service")
	}
	if svc.Status == "New" || svc.Status == "CreateFailed" || svc.Status == "Deleted" {
		return nil, nil
	}
	n, err := namesFor(svc.ID, a.id)
	if err != nil {
		return nil, err
	}
	if svc.AgentInstanceID == nil || *svc.AgentInstanceID != n.Bucket {
		return nil, ErrOwnership
	}
	if _, err = a.ownedBucket(ctx, n, svc.ID); err != nil {
		return nil, err
	}
	usage, err := a.backend.Usage(ctx, n.Bucket)
	if err != nil {
		return nil, err
	}
	if usage.Quota == 0 {
		return nil, errors.New("mandatory backend quota is absent")
	}
	values := []struct {
		name  string
		value float64
	}{
		{"s3.storage.used_bytes", float64(usage.Used)},
		{"s3.storage.quota_bytes", float64(usage.Quota)},
		{"s3.storage.usage_ratio", float64(usage.Used) / float64(usage.Quota)},
	}
	result := make([]agent.MetricEntry, 0, len(values))
	for _, v := range values {
		result = append(result, agent.MetricEntry{TypeName: v.name, Value: v.value, AgentInstanceID: n.Bucket, ResourceID: n.Bucket})
	}
	return result, nil
}

func opError(op string, err error) error { return fmt.Errorf("%s: %w", op, err) }
