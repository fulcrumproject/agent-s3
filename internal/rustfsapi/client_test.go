// Transport tests verify wire formats and prevent response/error secret disclosure.
// They also ensure mandatory quota fields and ownership documents fail closed.
package rustfsapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fulcrumproject/agent-s3/internal/config"
	"github.com/fulcrumproject/agent-s3/internal/s3agent"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(config.Backend{Endpoint: server.URL, ClientEndpoint: "http://customer.invalid", AccessKey: "test-admin", SecretKey: "test-admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAdministrativeSigningAndSecretWireFormat(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) {
			t.Error("invalid payload digest")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("missing SigV4")
		}
		if r.URL.Path != "/rustfs/admin/v3/set-user-secret-key" || r.URL.Query().Get("accessKey") != "customer" {
			t.Error("wrong administrative target")
		}
		if string(body) != `{"secret_key":"test-replacement"}` {
			t.Error("wrong reset wire format")
		}
		w.WriteHeader(200)
	})
	if err := c.SetSecret(context.Background(), "customer", "test-replacement"); err != nil {
		t.Fatal(err)
	}
}
func TestErrorPayloadIsNeverReturned(t *testing.T) {
	sentinel := "REMOTE_SECRET_SENTINEL"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, "<Error><Code>"+sentinel+"</Code><Message>"+sentinel+"</Message></Error>")
	})
	err := c.Ping(context.Background())
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatal("unsafe error")
	}
}
func TestNoRedirects(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) })
	if c.Ping(context.Background()) == nil || reached {
		t.Fatal("administrative redirect followed")
	}
}
func TestMissingStatisticsAndQuotaAreNotZero(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) })
	if _, e := c.GetQuota(context.Background(), "bucket"); e == nil {
		t.Fatal("missing quota accepted")
	}
	if _, e := c.Usage(context.Background(), "bucket"); e == nil {
		t.Fatal("missing usage accepted")
	}
}
func TestUnrecognizedPolicyFieldsRejectOwnership(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"policy_name":"owned","policy":{"Version":"2012-10-17","Statement":[],"UnexpectedGrant":true}}`)
	})
	if _, e := c.GetPolicy(context.Background(), "owned"); !errors.Is(e, s3agent.ErrOwnership) {
		t.Fatalf("got %v", e)
	}
}
func TestContextAndNotFound(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("accessKey") == "missing" {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "<Error><Code>NoSuchResource</Code></Error>")
			return
		}
		<-r.Context().Done()
	})
	if _, e := c.GetUser(context.Background(), "missing"); !errors.Is(e, s3agent.ErrNotFound) {
		t.Fatalf("got %v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := c.Ping(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("got %v", e)
	}
}
func TestPurgeDoesNotBypassRetentionOrHideBatchErrors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><Bucket>bucket</Bucket><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		case r.URL.Query().Has("versions"):
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>retained</Key><VersionId>v1</VersionId><IsLatest>true</IsLatest></Version></ListVersionsResult>`)
		case r.URL.Query().Has("delete"):
			if r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
				t.Error("retention bypass requested")
			}
			_, _ = io.WriteString(w, `<DeleteResult><Error><Key>retained</Key><VersionId>v1</VersionId><Code>AccessDenied</Code></Error></DeleteResult>`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(500)
		}
	})
	if err := c.PurgeBucket(context.Background(), "bucket"); err == nil {
		t.Fatal("retained data reported deleted")
	}
}
