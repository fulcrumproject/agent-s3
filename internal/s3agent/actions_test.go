// Tests exercise lifecycle invariants without a network or a running core.
// Failures model partial effects, ownership conflicts and retries explicitly.
package s3agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fulcrumproject/agent-lib-go/pkg/agent"
	"github.com/fulcrumproject/agent-s3/internal/config"
)

const testAgentID = "11111111-1111-4111-8111-111111111111"
const testServiceID = "22222222-2222-4222-8222-222222222222"

type fakeBackend struct {
	tags        map[string]string
	policy      *Policy
	user        *User
	secret      string
	quota       uint64
	used        uint64
	fail        string
	writes      []string
	creates     int
	userCreates int
}

func (f *fakeBackend) write(op string) error {
	f.writes = append(f.writes, op)
	if f.fail == op {
		f.fail = ""
		return errors.New("injected backend failure")
	}
	return nil
}
func (f *fakeBackend) Ping(context.Context) error { return nil }
func (f *fakeBackend) BucketTags(context.Context, string) (map[string]string, error) {
	if f.tags == nil {
		return nil, ErrNotFound
	}
	out := map[string]string{}
	for k, v := range f.tags {
		out[k] = v
	}
	return out, nil
}
func (f *fakeBackend) CreateBucket(context.Context, string) error {
	if e := f.write("createBucket"); e != nil {
		return e
	}
	f.tags = map[string]string{}
	f.creates++
	return nil
}
func (f *fakeBackend) PutBucketTags(_ context.Context, _ string, tags map[string]string) error {
	if e := f.write("tags:" + tags[tagPhase]); e != nil {
		return e
	}
	f.tags = tags
	return nil
}
func (f *fakeBackend) DeleteBucket(context.Context, string) error {
	if e := f.write("deleteBucket"); e != nil {
		return e
	}
	f.tags = nil
	return nil
}
func (f *fakeBackend) PurgeBucket(context.Context, string) error { return f.write("purge") }
func (f *fakeBackend) GetPolicy(context.Context, string) (Policy, error) {
	if f.policy == nil {
		return Policy{}, ErrNotFound
	}
	return *f.policy, nil
}
func (f *fakeBackend) PutPolicy(_ context.Context, _ string, p Policy) error {
	if e := f.write("policy"); e != nil {
		return e
	}
	f.policy = &p
	return nil
}
func (f *fakeBackend) DeletePolicy(context.Context, string) error {
	if e := f.write("deletePolicy"); e != nil {
		return e
	}
	f.policy = nil
	return nil
}
func (f *fakeBackend) GetUser(context.Context, string) (User, error) {
	if f.user == nil {
		return User{}, ErrNotFound
	}
	return *f.user, nil
}
func (f *fakeBackend) CreateUser(_ context.Context, _ string, secret string) error {
	if e := f.write("createUser"); e != nil {
		return e
	}
	f.user = &User{Status: "disabled"}
	f.secret = secret
	f.userCreates++
	return nil
}
func (f *fakeBackend) BindPolicy(_ context.Context, _ string, p string) error {
	if e := f.write("bind"); e != nil {
		return e
	}
	f.user.PolicyName = p
	return nil
}
func (f *fakeBackend) SetUserStatus(_ context.Context, _ string, enabled bool) error {
	op := "disable"
	if enabled {
		op = "enable"
	}
	if e := f.write(op); e != nil {
		return e
	}
	f.user.Status = "disabled"
	if enabled {
		f.user.Status = "enabled"
	}
	return nil
}
func (f *fakeBackend) SetSecret(_ context.Context, _ string, secret string) error {
	if e := f.write("secret"); e != nil {
		return e
	}
	f.secret = secret
	return nil
}
func (f *fakeBackend) DeleteUser(context.Context, string) error {
	if e := f.write("deleteUser"); e != nil {
		return e
	}
	f.user = nil
	f.secret = ""
	return nil
}
func (f *fakeBackend) GetQuota(context.Context, string) (uint64, error) { return f.quota, nil }
func (f *fakeBackend) SetQuota(_ context.Context, _ string, q uint64) error {
	if e := f.write("quota"); e != nil {
		return e
	}
	f.quota = q
	return nil
}
func (f *fakeBackend) Usage(context.Context, string) (Usage, error) {
	return Usage{Used: f.used, Quota: f.quota}, nil
}

func testAgent() (*Agent, *fakeBackend) {
	b := &fakeBackend{}
	return &Agent{id: testAgentID, backend: b, config: config.Backend{ClientEndpoint: "http://customer.invalid", Region: "us-east-1", OperationTimeoutSeconds: 5}}, b
}
func testJob(action string, props *Properties) *agent.Job[Properties, Properties, Resources] {
	return &agent.Job[Properties, Properties, Resources]{ID: "job", Action: action, Params: props,
		Service: &agent.Service[Properties, Resources]{ID: testServiceID, AgentID: testAgentID, Status: "New", Properties: &Properties{QuotaBytes: 1024}}}
}
func createJob() *agent.Job[Properties, Properties, Resources] {
	secret := "test-secret-sentinel-not-a-real-credential"
	return testJob("create", &Properties{QuotaBytes: 1024, SecretAccessKey: &secret})
}
func mustCreate(t *testing.T, a *Agent) {
	t.Helper()
	if _, e := a.Handle(context.Background(), createJob()); e != nil {
		t.Fatal(e)
	}
}

func TestCreateAndRetryAreIdempotentAndSecretFree(t *testing.T) {
	a, b := testAgent()
	job := createJob()
	result, e := a.Handle(context.Background(), job)
	if e != nil {
		t.Fatal(e)
	}
	if b.user.Status != "enabled" || b.quota != 1024 || b.tags[tagPhase] != active {
		t.Fatal("incomplete provisioning")
	}
	quotaIndex, enableIndex := -1, -1
	for i, op := range b.writes {
		if op == "quota" {
			quotaIndex = i
		}
		if op == "enable" {
			enableIndex = i
		}
	}
	if quotaIndex < 0 || enableIndex < quotaIndex {
		t.Fatal("credentials enabled before quota")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), *job.Params.SecretAccessKey) || strings.Contains(string(encoded), "secretAccessKey") {
		t.Fatal("secret leaked into runtime")
	}
	if _, e = a.Handle(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	if b.creates != 1 || b.userCreates != 1 {
		t.Fatal("duplicate resources")
	}
	// A completed backend operation with a lost core acknowledgement needs no secret.
	job.Params.SecretAccessKey = nil
	if _, e = a.Handle(context.Background(), job); e != nil {
		t.Fatal(e)
	}
}

func TestQuotaFailureNeverEnablesAndCanBeRetried(t *testing.T) {
	a, b := testAgent()
	b.fail = "quota"
	if _, e := a.Handle(context.Background(), createJob()); e == nil {
		t.Fatal("failure hidden")
	}
	if b.user.Status != "disabled" || b.tags[tagPhase] != provisioning {
		t.Fatal("unsafe partial provisioning")
	}
	for _, op := range b.writes {
		if op == "enable" {
			t.Fatal("enabled on quota failure")
		}
	}
	mustCreate(t, a)
	if b.creates != 1 || b.userCreates != 1 {
		t.Fatal("retry duplicated resources")
	}
}

func TestAmbiguousOwnershipIsNeverAdopted(t *testing.T) {
	for _, failure := range []string{"tags:" + provisioning, "bind"} {
		t.Run(failure, func(t *testing.T) {
			a, b := testAgent()
			b.fail = failure
			if _, e := a.Handle(context.Background(), createJob()); e == nil {
				t.Fatal("failure hidden")
			}
			before := len(b.writes)
			if _, e := a.Handle(context.Background(), createJob()); !errors.Is(e, ErrOwnership) {
				t.Fatalf("got %v", e)
			}
			if len(b.writes) != before {
				t.Fatal("uncertain resource was mutated")
			}
		})
	}
}

func TestForeignBucketBlocksAllMutations(t *testing.T) {
	for _, action := range []string{"create", "update", "suspend", "resume", "delete"} {
		t.Run(action, func(t *testing.T) {
			a, b := testAgent()
			b.tags = map[string]string{tagService: testServiceID, tagAgent: "another-agent", tagPhase: active}
			job := createJob()
			job.Action = action
			job.Service.Status = "Active"
			if _, e := a.Handle(context.Background(), job); !errors.Is(e, ErrOwnership) {
				t.Fatalf("got %v", e)
			}
			if len(b.writes) != 0 {
				t.Fatal("foreign resource mutated")
			}
		})
	}
}
func TestPolicyDriftAndForeignUsersBlockDelete(t *testing.T) {
	for _, kind := range []string{"policy", "group", "binding"} {
		t.Run(kind, func(t *testing.T) {
			a, b := testAgent()
			mustCreate(t, a)
			switch kind {
			case "policy":
				b.policy.Statement[0].Effect = "Allow"
			case "group":
				b.user.MemberOf = []string{"foreign"}
			case "binding":
				b.user.PolicyName = "foreign"
			}
			before := len(b.writes)
			if _, e := a.Handle(context.Background(), testJob("delete", nil)); !errors.Is(e, ErrOwnership) {
				t.Fatalf("got %v", e)
			}
			if len(b.writes) != before {
				t.Fatal("foreign IAM mutated")
			}
		})
	}
}

func TestPartialDeleteStaysDisabledAndOnlyDeleteCanContinue(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	b.fail = "purge"
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e == nil {
		t.Fatal("purge failure hidden")
	}
	if b.tags[tagPhase] != deleting || b.user.Status != "disabled" {
		t.Fatal("missing durable deletion barrier")
	}
	for _, action := range []string{"resume", "suspend", "update", "create"} {
		job := createJob()
		job.Action = action
		job.Service.Status = "Active"
		before := len(b.writes)
		if _, e := a.Handle(context.Background(), job); !errors.Is(e, ErrDeleting) {
			t.Fatalf("%s: %v", action, e)
		}
		if len(b.writes) != before {
			t.Fatal("deleting service mutated by non-delete action")
		}
	}
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e != nil {
		t.Fatal(e)
	}
	if b.tags != nil || b.policy != nil || b.user != nil {
		t.Fatal("orphan resources")
	}
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e != nil {
		t.Fatal(e)
	}
}

func TestDeleteKeepsOwnershipUntilIAMCleanupCompletes(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	b.fail = "deletePolicy"
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e == nil {
		t.Fatal("failure hidden")
	}
	if b.tags == nil || b.tags[tagPhase] != deleting || b.user != nil {
		t.Fatal("lost ownership evidence")
	}
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e != nil {
		t.Fatal(e)
	}
}
func TestUpdateUsesBackendQuotaAndPreservesSuspension(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	if _, e := a.Handle(context.Background(), testJob("suspend", nil)); e != nil {
		t.Fatal(e)
	}
	secret := "replacement-test-secret-not-a-real-credential"
	job := testJob("update", &Properties{QuotaBytes: 2048, SecretAccessKey: &secret})
	job.Service.Status = "Suspended"
	if _, e := a.Handle(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	if b.user.Status != "disabled" || b.secret != secret || b.quota != 2048 {
		t.Fatal("update changed suspension or lost properties")
	}
	before := len(b.writes)
	job.Params.QuotaBytes = 1024
	if _, e := a.Handle(context.Background(), job); !errors.Is(e, ErrQuotaDecrease) {
		t.Fatalf("got %v", e)
	}
	if len(b.writes) != before || b.quota != 2048 {
		t.Fatal("decrease changed backend")
	}
	if _, e := a.Handle(context.Background(), testJob("resume", nil)); e != nil {
		t.Fatal(e)
	}
	if b.user.Status != "enabled" {
		t.Fatal("resume failed")
	}
}
func TestUpdateRecoversFailedCreateWithResentSecret(t *testing.T) {
	a, b := testAgent()
	b.fail = "quota"
	_, _ = a.Handle(context.Background(), createJob())
	job := createJob()
	job.Action = "update"
	job.Service.Status = "CreateFailed"
	if _, e := a.Handle(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	if b.user.Status != "enabled" || b.creates != 1 {
		t.Fatal("failed recovery")
	}
}
func TestValidationRejectsBeforeCreatingAnything(t *testing.T) {
	for _, kind := range []string{"nilJob", "noSecret", "zeroQuota", "largeQuota", "vaultReference", "wrongAgent", "invalidID"} {
		t.Run(kind, func(t *testing.T) {
			a, b := testAgent()
			job := createJob()
			switch kind {
			case "nilJob":
				job = nil
			case "noSecret":
				job.Params.SecretAccessKey = nil
			case "zeroQuota":
				job.Params.QuotaBytes = -1
			case "largeQuota":
				job.Params.QuotaBytes = MaxQuota + 1
			case "vaultReference":
				v := "vault://unresolved"
				job.Params.SecretAccessKey = &v
			case "wrongAgent":
				job.Service.AgentID = "33333333-3333-4333-8333-333333333333"
			case "invalidID":
				job.Service.ID = "../../foreign"
			}
			if _, e := a.Handle(context.Background(), job); e == nil {
				t.Fatal("invalid input accepted")
			}
			if len(b.writes) != 0 {
				t.Fatal("invalid request caused writes")
			}
		})
	}
}
func TestMetricsUseObservedValues(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	b.used = 2048
	n, _ := namesFor(testServiceID, testAgentID)
	svc := &agent.Service[Properties, Resources]{ID: testServiceID, AgentID: testAgentID, Status: "Active", AgentInstanceID: &n.Bucket}
	metrics, e := a.Metrics(context.Background(), svc)
	if e != nil {
		t.Fatal(e)
	}
	if len(metrics) != 3 || metrics[2].Value != 2 {
		t.Fatal("usage was hidden or clamped")
	}
	b.quota = 0
	if metrics, e = a.Metrics(context.Background(), svc); e == nil || len(metrics) != 0 {
		t.Fatal("missing quota reported as zero metrics")
	}
}
