"""Negative acceptance checks against the isolated RustFS fixture.
Failures are blockers, not expected-failure annotations or permission exceptions.
"""
import os
import secrets
import unittest
import uuid

from backend_contract import admin, expect_admin, fixture_credentials, signed_request, require_fixtures


def restricted_policy(bucket):
    return {"Version": "2012-10-17", "Statement": [
        {"Effect": "Deny", "Action": ["admin:*"]},
        {"Effect": "Deny", "Action": ["sts:*"]},
        {"Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": ["arn:aws:s3:::" + bucket]},
    ]}


class IAMBoundaryTests(unittest.TestCase):
    def setUp(self):
        require_fixtures()
        self.endpoint = "http://127.0.0.1:" + str(int(os.environ.get("RUSTFS_TEST_PORT", "19000")))
        self.customer_endpoint = "http://127.0.0.1:" + str(int(os.environ.get("S3_CLIENT_PORT", "19080")))
        self.root_key, self.root_secret = fixture_credentials()

    def root_call(self, method, route, query=None, body=None):
        return expect_admin(self.endpoint, self.root_key, self.root_secret, method, route, query, body)

    def test_customer_boundary_prevents_iam_self_secret_change(self):
        """The gateway, not the backend self-service policy, must enforce this denial."""
        identity = uuid.uuid4().hex
        key, secret, replacement = "fc" + identity, secrets.token_hex(32), secrets.token_hex(32)
        policy_name = "fc-boundary-" + identity
        user_created = False
        self.root_call("PUT", "add-canned-policy", {"name": policy_name},
                       restricted_policy("fulcrum-boundary-" + identity))
        try:
            self.root_call("PUT", "add-user", {"accessKey": key},
                           {"secretKey": secret, "status": "enabled", "policy": policy_name})
            user_created = True
            self.root_call("PUT", "set-user-or-group-policy",
                           {"policyName": policy_name, "userOrGroup": key, "isGroup": "false"})
            status, _ = admin(self.customer_endpoint, key, secret, "POST", "account/password",
                              body={"current_secret_key": secret, "new_secret_key": replacement})
            replacement_status, _ = admin(self.endpoint, key, replacement, "GET", "account/info")
            self.assertEqual(status, 403,
                             "self-service mutation was not denied; new credential authentication HTTP "
                             + str(replacement_status))
            self.assertEqual(replacement_status, 403, "denied mutation changed the backend secret")
        finally:
            if user_created:
                self.root_call("DELETE", "remove-user", {"accessKey": key})
            self.root_call("DELETE", "remove-canned-policy", {"name": policy_name})

    def test_restricted_service_account_cannot_delete_sibling(self):
        """The customer boundary must block even the known shared-parent backend fallback."""
        created = []
        try:
            for _ in range(2):
                identity = uuid.uuid4().hex
                key, secret = "fc" + identity, secrets.token_hex(32)
                self.root_call("PUT", "add-service-account", body={
                    "accessKey": key, "secretKey": secret,
                    "name": "fc-boundary-" + identity[:18],
                    "policy": restricted_policy("fulcrum-boundary-" + identity),
                })
                created.append((key, secret))
            key_a, secret_a = created[0]
            key_b, _ = created[1]
            status, _ = admin(self.customer_endpoint, key_a, secret_a, "DELETE", "delete-service-account",
                              {"accessKey": key_b})
            remaining_status, _ = admin(self.endpoint, self.root_key, self.root_secret,
                                        "GET", "info-service-account", {"accessKey": key_b})
            self.assertEqual(remaining_status, 200,
                             "sibling credential disappeared; customer deletion returned HTTP " + str(status))
            self.assertIn(status, (400, 403, 404), "customer administrative deletion was accepted")
        finally:
            # Only accounts created by this invocation; tolerate B already deleted by the probe.
            for key, _ in created:
                status, _ = admin(self.endpoint, self.root_key, self.root_secret,
                                  "GET", "info-service-account", {"accessKey": key})
                if status == 200:
                    self.root_call("DELETE", "delete-service-account", {"accessKey": key})

    def test_gateway_rejects_admin_route_aliases_even_with_root_signature(self):
        # Root signing is intentional: these denials must come from the boundary, not IAM.
        for path in (
            "/rustfs/admin/v3/list-users",
            "/minio/admin/v3/list-users",
            "//rustfs/admin/v3/list-users",
            "/rustfs//admin/v3/list-users",
            "/%72ustfs/admin/v3/list-users",
            "/rustfs%2fadmin%2fv3%2flist-users",
            "/fulcrum-probe/../rustfs/admin/v3/list-users",
            "/fulcrum-probe/%2e%2e/rustfs/admin/v3/list-users",
        ):
            with self.subTest(path=path):
                status, _ = signed_request(self.customer_endpoint, self.root_key, self.root_secret,
                                            "GET", path)
                self.assertIn(status, (400, 403), "administrative route reached the backend")


if __name__ == "__main__":
    unittest.main(verbosity=2)
