// Service models keep secrets out of job responses and runtime data.
// Resource identifiers are deterministic, but ownership is verified separately.
package s3agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

const MaxQuota = int64(1<<53 - 1)
const (
	tagService   = "fulcrum-service"
	tagAgent     = "fulcrum-agent"
	tagPhase     = "fulcrum-phase"
	provisioning = "provisioning"
	active       = "active"
	deleting     = "deleting"
)

type Properties struct {
	QuotaBytes      int64   `json:"quotaBytes"`
	SecretAccessKey *string `json:"secretAccessKey,omitempty"`
}
type Resources struct {
	Bucket      string `json:"bucket"`
	AccessKeyID string `json:"accessKeyId"`
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	QuotaBytes  uint64 `json:"quotaBytes"`
}
type Names struct{ Bucket, Key, Policy, Owner string }

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validID(id string) bool {
	return uuidPattern.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func namesFor(serviceID, agentID string) (Names, error) {
	if !validID(serviceID) || !validID(agentID) {
		return Names{}, errors.New("canonical service and agent UUIDs are required")
	}
	compact := strings.ReplaceAll(serviceID, "-", "")
	return Names{
		Bucket: "fulcrum-" + serviceID, Key: "fc" + compact, Policy: "fc-" + compact,
		Owner: "Owner" + strings.ReplaceAll(agentID, "-", "") + compact,
	}, nil
}

type Policy struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}
type Statement struct {
	Sid      string   `json:"Sid"`
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource []string `json:"Resource,omitempty"`
}

func policyFor(n Names) Policy {
	return Policy{Version: "2012-10-17", Statement: []Statement{
		{Sid: n.Owner, Effect: "Deny", Action: []string{"admin:*"}},
		{Sid: "NoSessions", Effect: "Deny", Action: []string{"sts:*"}},
		{Sid: "Bucket", Effect: "Allow", Action: []string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"}, Resource: []string{"arn:aws:s3:::" + n.Bucket}},
		{Sid: "Objects", Effect: "Allow", Action: []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"}, Resource: []string{"arn:aws:s3:::" + n.Bucket + "/*"}},
	}}
}

func canonicalPolicy(p Policy) string {
	p.Statement = append([]Statement(nil), p.Statement...)
	for i := range p.Statement {
		s := &p.Statement[i]
		s.Action = append([]string(nil), s.Action...)
		sort.Strings(s.Action)
		s.Resource = append([]string(nil), s.Resource...)
		sort.Strings(s.Resource)
	}
	sort.Slice(p.Statement, func(i, j int) bool { return p.Statement[i].Sid < p.Statement[j].Sid })
	data, _ := json.Marshal(p)
	return string(data)
}
