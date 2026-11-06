// Configuration tests reject ambiguous endpoints and unresolved credentials.
// No test logs or persists administrative input.
package config

import "testing"

func TestBackendValidation(t *testing.T) {
	good := Backend{Endpoint: "http://127.0.0.1:9000/", ClientEndpoint: "http://127.0.0.1:9080/", AccessKey: "test", SecretKey: "test-only"}
	if err := good.Normalize(); err != nil {
		t.Fatal(err)
	}
	if good.Region != "us-east-1" || good.HTTPTimeoutSeconds != 15 || good.OperationTimeoutSeconds != 180 {
		t.Fatal("defaults not applied")
	}
	for _, bad := range []string{"", "ftp://host", "http://user:pass@host", "http://host/prefix", "http://host?secret=x", "http://host?", "http://host#", "http://host#fragment"} {
		if _, err := Endpoint(bad); err == nil {
			t.Fatalf("accepted invalid endpoint")
		}
	}
	same := good
	same.ClientEndpoint = same.Endpoint
	if err := same.Normalize(); err == nil {
		t.Fatal("direct backend advertised to customers")
	}
	missing := good
	missing.SecretKey = "vault://unresolved"
	if err := missing.Normalize(); err == nil {
		t.Fatal("unresolved secret accepted")
	}
}
