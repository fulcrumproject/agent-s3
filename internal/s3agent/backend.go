// Backend is the narrow API boundary used by the S3 handlers.
// Concrete RustFS transport and tests implement the same interface.
package s3agent

import (
	"context"
	"errors"
)

var (
	ErrNotFound       = errors.New("backend resource not found")
	ErrNotReady       = errors.New("backend temporarily unavailable")
	ErrOwnership      = errors.New("resource ownership or policy does not match this service; operator reconciliation required")
	ErrDeleting       = errors.New("deletion has started; only delete may continue")
	ErrQuotaDecrease  = errors.New("quota reduction is not supported; backend quota unchanged")
	ErrSecretRequired = errors.New("resend a valid ephemeral secret to provision or recover this service")
)

type User struct {
	PolicyName string   `json:"policyName"`
	Status     string   `json:"status"`
	MemberOf   []string `json:"memberOf"`
}
type Usage struct{ Used, Quota uint64 }

type Backend interface {
	Ping(context.Context) error
	BucketTags(context.Context, string) (map[string]string, error)
	CreateBucket(context.Context, string) error
	PutBucketTags(context.Context, string, map[string]string) error
	DeleteBucket(context.Context, string) error
	PurgeBucket(context.Context, string) error
	GetPolicy(context.Context, string) (Policy, error)
	PutPolicy(context.Context, string, Policy) error
	DeletePolicy(context.Context, string) error
	GetUser(context.Context, string) (User, error)
	CreateUser(context.Context, string, string) error
	BindPolicy(context.Context, string, string) error
	SetUserStatus(context.Context, string, bool) error
	SetSecret(context.Context, string, string) error
	DeleteUser(context.Context, string) error
	GetQuota(context.Context, string) (uint64, error)
	SetQuota(context.Context, string, uint64) error
	Usage(context.Context, string) (Usage, error)
}
