// Package rustfsapi implements the version-checked RustFS administrative transport.
// Responses and transport errors are sanitized before crossing the agent boundary.
package rustfsapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/fulcrumproject/agent-s3/internal/config"
	"github.com/fulcrumproject/agent-s3/internal/s3agent"
)

const maxResponse = 1 << 20

type Client struct {
	cfg         config.Backend
	http        *http.Client
	s3          *s3.Client
	signer      *v4.Signer
	credentials aws.Credentials
}

var _ s3agent.Backend = (*Client)(nil)

func New(cfg config.Backend) (*Client, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Do not send administrative credentials through ambient proxy variables.
	hc := &http.Client{
		Timeout: time.Duration(cfg.HTTPTimeoutSeconds) * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cred := aws.Credentials{AccessKeyID: cfg.AccessKey, SecretAccessKey: cfg.SecretKey}
	return &Client{
		cfg: cfg, http: hc, signer: v4.NewSigner(), credentials: cred,
		s3: s3.New(s3.Options{
			Region: cfg.Region, BaseEndpoint: aws.String(cfg.Endpoint), UsePathStyle: true,
			Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
			HTTPClient:  hc, RetryMaxAttempts: 3,
		}),
	}, nil
}

type APIError struct {
	Operation string
	Status    int
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return e.Operation + ": backend transport failed"
	}
	return fmt.Sprintf("%s: backend request failed (HTTP %d)", e.Operation, e.Status)
}

func safeError(ctx context.Context, operation string, status int, code string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch code {
	case "NoSuchBucket", "NoSuchKey", "NoSuchResource", "NoSuchUser", "NoSuchPolicy", "NoSuchUpload", "NotFound":
		return fmt.Errorf("%s: %w", operation, s3agent.ErrNotFound)
	case "ServiceUnavailable", "SlowDown":
		return fmt.Errorf("%s: %w", operation, s3agent.ErrNotReady)
	}
	if status == 503 || status == 429 {
		return fmt.Errorf("%s: %w", operation, s3agent.ErrNotReady)
	}
	return &APIError{Operation: operation, Status: status}
}

func sdkError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	var api smithy.APIError
	code := ""
	if errors.As(err, &api) {
		code = api.ErrorCode()
	}
	status := 0
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		status = response.HTTPStatusCode()
	}
	return safeError(ctx, operation, status, code)
}

func (c *Client) admin(ctx context.Context, method, route string, query url.Values, body, result any) error {
	data := []byte{}
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return errors.New("cannot encode administrative request")
		}
	}
	target := c.cfg.Endpoint + "/rustfs/admin/v3/" + route
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(data))
	if err != nil {
		return errors.New("cannot construct administrative request")
	}
	req.Header.Set("Content-Type", "application/json")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", digest)
	if err = c.signer.SignHTTP(ctx, c.credentials, req, digest, "s3", c.cfg.Region, time.Now()); err != nil {
		return errors.New("cannot sign administrative request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return safeError(ctx, route, 0, "")
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(payload) > maxResponse {
		return errors.New("invalid administrative response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var wire struct {
			Code string `xml:"Code"`
		}
		_ = xml.Unmarshal(payload, &wire)
		code := ""
		// A missing S3 object/bucket is NOT a missing IAM resource. A wrong S3
		// endpoint must never turn administrative writes into ordinary objects.
		switch wire.Code {
		case "NoSuchResource", "NoSuchUser", "NoSuchPolicy":
			if resp.StatusCode == http.StatusNotFound {
				code = "NoSuchResource"
			}
		case "ServiceUnavailable", "SlowDown":
			code = wire.Code
		}
		return safeError(ctx, route, resp.StatusCode, code)
	}
	if result != nil {
		if err = json.Unmarshal(payload, result); err != nil {
			return errors.New("invalid administrative JSON response")
		}
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	var info struct {
		AccessKey        string `json:"access_key"`
		SessionAccessKey string `json:"session_access_key"`
		Status           string `json:"status"`
	}
	if err := c.admin(ctx, "GET", "account/info", nil, nil, &info); err != nil {
		return err
	}
	if info.Status != "enabled" || (info.AccessKey != c.cfg.AccessKey && info.SessionAccessKey != c.cfg.AccessKey) {
		return errors.New("RustFS administrative identity check failed")
	}
	return nil
}
func (c *Client) GetUser(ctx context.Context, key string) (s3agent.User, error) {
	var result s3agent.User
	err := c.admin(ctx, "GET", "user-info", url.Values{"accessKey": {key}}, nil, &result)
	return result, err
}
func (c *Client) CreateUser(ctx context.Context, key, secret string) error {
	return c.admin(ctx, "PUT", "add-user", url.Values{"accessKey": {key}},
		map[string]any{"secretKey": secret, "status": "disabled"}, nil)
}
func (c *Client) BindPolicy(ctx context.Context, key, policy string) error {
	return c.admin(ctx, "PUT", "set-user-or-group-policy",
		url.Values{"policyName": {policy}, "userOrGroup": {key}, "isGroup": {"false"}}, nil, nil)
}
func (c *Client) SetUserStatus(ctx context.Context, key string, enabled bool) error {
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	return c.admin(ctx, "PUT", "set-user-status", url.Values{"accessKey": {key}, "status": {status}}, nil, nil)
}
func (c *Client) SetSecret(ctx context.Context, key, secret string) error {
	return c.admin(ctx, "PUT", "set-user-secret-key", url.Values{"accessKey": {key}},
		map[string]string{"secret_key": secret}, nil)
}
func (c *Client) DeleteUser(ctx context.Context, key string) error {
	return c.admin(ctx, "DELETE", "remove-user", url.Values{"accessKey": {key}}, nil, nil)
}
func (c *Client) GetPolicy(ctx context.Context, name string) (s3agent.Policy, error) {
	var wrapper struct {
		Name   string          `json:"policy_name"`
		Policy json.RawMessage `json:"policy"`
	}
	if err := c.admin(ctx, "GET", "info-canned-policy", url.Values{"name": {name}}, nil, &wrapper); err != nil {
		return s3agent.Policy{}, err
	}
	if wrapper.Name != name {
		return s3agent.Policy{}, s3agent.ErrOwnership
	}
	var policy s3agent.Policy
	decoder := json.NewDecoder(bytes.NewReader(wrapper.Policy))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return s3agent.Policy{}, s3agent.ErrOwnership
	}
	return policy, nil
}
func (c *Client) PutPolicy(ctx context.Context, name string, policy s3agent.Policy) error {
	return c.admin(ctx, "PUT", "add-canned-policy", url.Values{"name": {name}}, policy, nil)
}
func (c *Client) DeletePolicy(ctx context.Context, name string) error {
	return c.admin(ctx, "DELETE", "remove-canned-policy", url.Values{"name": {name}}, nil, nil)
}
func (c *Client) GetQuota(ctx context.Context, bucket string) (uint64, error) {
	var result struct {
		Quota *uint64 `json:"quota"`
	}
	if err := c.admin(ctx, "GET", "get-bucket-quota", url.Values{"bucket": {bucket}}, nil, &result); err != nil {
		return 0, err
	}
	if result.Quota == nil {
		return 0, errors.New("quota missing from backend response")
	}
	return *result.Quota, nil
}
func (c *Client) SetQuota(ctx context.Context, bucket string, quota uint64) error {
	if quota == 0 || quota > uint64(s3agent.MaxQuota) {
		return errors.New("invalid quota")
	}
	return c.admin(ctx, "PUT", "quota/"+url.PathEscape(bucket), nil,
		map[string]any{"quota": quota, "quota_type": "HARD", "reservation_protocol": 1}, nil)
}
func (c *Client) Usage(ctx context.Context, bucket string) (s3agent.Usage, error) {
	var result struct {
		Used  *uint64 `json:"current_usage"`
		Quota *uint64 `json:"quota_limit"`
	}
	if err := c.admin(ctx, "GET", "quota-stats/"+url.PathEscape(bucket), nil, nil, &result); err != nil {
		return s3agent.Usage{}, err
	}
	if result.Used == nil || result.Quota == nil || *result.Quota == 0 {
		return s3agent.Usage{}, errors.New("incomplete quota statistics")
	}
	return s3agent.Usage{Used: *result.Used, Quota: *result.Quota}, nil
}
