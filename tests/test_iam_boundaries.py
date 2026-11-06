"""Negative acceptance checks against the isolated RustFS fixture.
Failures are blockers, not expected-failure annotations or permission exceptions.
"""
import os
import secrets
import unittest
import uuid

from backend_contract import admin, expect_admin, fixture_credentials


def restricted_policy(bucket):
    return {"Version": "2012-10-17", "Statement": [
        {"Effect": "Deny", "Action": ["admin:*"]},
        {"Effect": "Deny", "Action": ["sts:*"]},
        {"Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": ["arn:aws:s3:::" + bucket]},
    ]}


class IAMBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.endpoint = "http://127.0.0.1:" + str(int(os.environ.get("RUSTFS_TEST_PORT", "19000")))
        self.root_key, self.root_secret = fixture_credentials()

    def root_call(self, method, route, query=None, body=None):
        return expect_admin(self.endpoint, self.root_key, self.root_secret, method, route, query, body)

    def test_explicit_admin_deny_prevents_iam_self_secret_change(self):
        """Data-only IAM credentials must not change their own secret through an admin route."""
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
            status, _ = admin(self.endpoint, key, secret, "POST", "account/password",
                              body={"current_secret_key": secret, "new_secret_key": replacement})
            replacement_status, _ = admin(self.endpoint, key, replacement, "GET", "account/info")
            self.assertEqual(status, 403,
                             "self-service mutation was not denied; new credential authentication HTTP "
                             + str(replacement_status))
        finally:
            if user_created:
                self.root_call("DELETE", "remove-user", {"accessKey": key})
            self.root_call("DELETE", "remove-canned-policy", {"name": policy_name})

    def test_restricted_service_account_cannot_delete_sibling(self):
        """Do not use a shared administrative parent unless sibling credentials remain isolated."""
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
            status, _ = admin(self.endpoint, key_a, secret_a, "DELETE", "delete-service-account",
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


if __name__ == "__main__":
    unittest.main(verbosity=2)
