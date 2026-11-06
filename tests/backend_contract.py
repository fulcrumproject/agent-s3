#!/usr/bin/env python3
"""Exercise the pinned RustFS backend before claiming agent compatibility.
Uses only generated data and resources created by this invocation; never prints credentials.
"""
import hashlib
import json
import os
from pathlib import Path
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

import boto3
from botocore.auth import SigV4Auth
from botocore.awsrequest import AWSRequest
from botocore.config import Config
from botocore.credentials import Credentials
from botocore.exceptions import ClientError


class CheckFailure(Exception):
    pass


def fixture_credentials():
    path = Path(__file__).resolve().parents[1] / ".local" / "rustfs.env"
    values = dict(line.split("=", 1) for line in path.read_text().splitlines() if "=" in line)
    return values["RUSTFS_ACCESS_KEY"], values["RUSTFS_SECRET_KEY"]


def client(endpoint, key, secret):
    return boto3.client("s3", endpoint_url=endpoint, aws_access_key_id=key,
                        aws_secret_access_key=secret, region_name="us-east-1",
                        config=Config(signature_version="s3v4", s3={"addressing_style": "path"},
                                      connect_timeout=3, read_timeout=10,
                                      retries={"max_attempts": 0}))


def admin(endpoint, key, secret, method, route, query=None, body=None):
    url = endpoint + "/rustfs/admin/v3/" + route
    if query:
        url += "?" + urllib.parse.urlencode(query)
    payload = b"" if body is None else json.dumps(body, separators=(",", ":")).encode()
    headers = {"Content-Type": "application/json", "X-Amz-Content-SHA256": hashlib.sha256(payload).hexdigest()}
    request = AWSRequest(method=method, url=url, data=payload, headers=headers)
    SigV4Auth(Credentials(key, secret), "s3", "us-east-1").add_auth(request)
    signed = request.prepare()
    try:
        with urllib.request.urlopen(urllib.request.Request(signed.url, data=payload,
                                    headers=dict(signed.headers), method=method), timeout=15) as response:
            data = response.read()
            return response.status, json.loads(data) if data else None
    except urllib.error.HTTPError as error:
        # Only the status is safe for diagnostics. Never dump a response/request body.
        error.read()
        error.close()
        return error.code, None


def expect_admin(*args, **kwargs):
    status, body = admin(*args, **kwargs)
    if status != 200:
        raise CheckFailure("administrative operation " + args[4] + " returned HTTP " + str(status))
    return body


def deny(operation):
    try:
        operation()
    except ClientError as error:
        code = error.response["Error"]["Code"]
        if code not in ("AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "AllAccessDisabled") and not (
            code == "403" and error.response["ResponseMetadata"]["HTTPStatusCode"] == 403
        ):
            raise CheckFailure("unexpected denial code " + code) from None
    else:
        raise CheckFailure("forbidden operation unexpectedly succeeded")


def url_status(url, data=None):
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=data), timeout=10) as response:
            return response.status
    except urllib.error.HTTPError as error:
        error.read()
        error.close()
        return error.code


def run():
    endpoint = "http://127.0.0.1:" + os.environ.get("RUSTFS_TEST_PORT", "19000")
    root_key, root_secret = fixture_credentials()
    root = client(endpoint, root_key, root_secret)
    identity = uuid.uuid4().hex
    bucket = "fulcrum-probe-" + identity
    other = "fulcrum-probe-other-" + identity
    key = "fc" + identity
    secret = secrets.token_hex(32)
    policy_name = "fc-probe-" + identity
    created_buckets = []
    created_user = False
    created_policy = False
    uploads = []
    stage = "create bucket"
    try:
        for name in (bucket, other):
            root.create_bucket(Bucket=name)
            created_buckets.append(name)
        root.put_bucket_tagging(Bucket=bucket, Tagging={"TagSet": [{"Key": "fulcrum-service", "Value": identity}]})
        tags = root.get_bucket_tagging(Bucket=bucket)["TagSet"]
        if tags != [{"Key": "fulcrum-service", "Value": identity}]:
            raise CheckFailure("ownership tags did not round-trip")
        print("PASS bucket creation and ownership tags")

        stage = "set and read quota"
        limit = 1024 * 1024
        expect_admin(endpoint, root_key, root_secret, "PUT", "quota/" + bucket,
                     body={"quota": limit, "quota_type": "HARD"})
        quota = expect_admin(endpoint, root_key, root_secret, "GET", "get-bucket-quota", {"bucket": bucket})
        if quota.get("quota") != limit:
            raise CheckFailure("quota readback does not match")
        # Newly created buckets may need a scanner cycle before usage is available.
        for _ in range(30):
            status, stats = admin(endpoint, root_key, root_secret, "GET", "quota-stats/" + bucket)
            if status == 200 and stats.get("quota_limit") == limit:
                break
            time.sleep(1)
        else:
            raise CheckFailure("quota accounting did not become ready")
        print("PASS mandatory quota configuration, readback and accounting readiness")

        stage = "create isolated policy and disabled user"
        policy = {"Version": "2012-10-17", "Statement": [
            # RustFS requires separate action families; Admin/STS statements omit Resource.
            {"Sid": "NoAdministration", "Effect": "Deny", "Action": ["admin:*"]},
            {"Sid": "NoSessions", "Effect": "Deny", "Action": ["sts:*"]},
            {"Sid": "Bucket", "Effect": "Allow", "Action": ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"], "Resource": ["arn:aws:s3:::" + bucket]},
            {"Sid": "Objects", "Effect": "Allow", "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"], "Resource": ["arn:aws:s3:::" + bucket + "/*"]}
        ]}
        expect_admin(endpoint, root_key, root_secret, "PUT", "add-canned-policy", {"name": policy_name}, policy)
        created_policy = True
        expect_admin(endpoint, root_key, root_secret, "PUT", "add-user", {"accessKey": key},
                     {"secretKey": secret, "status": "disabled", "policy": policy_name})
        created_user = True
        expect_admin(endpoint, root_key, root_secret, "PUT", "set-user-or-group-policy",
                     {"policyName": policy_name, "userOrGroup": key, "isGroup": "false"})
        user = client(endpoint, key, secret)
        deny(lambda: user.list_objects_v2(Bucket=bucket))
        expect_admin(endpoint, root_key, root_secret, "PUT", "set-user-status", {"accessKey": key, "status": "enabled"})
        user.put_object(Bucket=bucket, Key="sample", Body=b"sample data")
        response = user.get_object(Bucket=bucket, Key="sample")
        if response["Body"].read() != b"sample data":
            raise CheckFailure("object content differs")
        response["Body"].close()
        deny(lambda: user.list_objects_v2(Bucket=other))
        visible_buckets = {item["Name"] for item in user.list_buckets().get("Buckets", [])}
        if visible_buckets != {bucket}:
            raise CheckFailure("bucket discovery exposed an unexpected scope")
        deny(lambda: user.put_object(Bucket=other, Key="forbidden", Body=b"no"))
        deny(lambda: user.put_bucket_tagging(Bucket=bucket, Tagging={"TagSet": [{"Key": "forged", "Value": "x"}]}))
        deny(lambda: user.put_bucket_policy(Bucket=bucket, Policy=json.dumps(policy)))
        status, _ = admin(endpoint, key, secret, "PUT", "add-user", {"accessKey": key}, {"secretKey": secret, "status": "enabled"})
        if status != 403:
            raise CheckFailure("customer add-user self-administration not denied (HTTP " + str(status) + ")")
        child = "fc-child-" + identity
        status, _ = admin(endpoint, key, secret, "PUT", "add-service-account", body={
            "accessKey": child, "secretKey": secrets.token_hex(32), "policy": policy,
        })
        if status == 200:
            admin(endpoint, root_key, root_secret, "DELETE", "delete-service-account", {"accessKey": child})
        if status != 403:
            raise CheckFailure("customer creation of additional credentials not denied (HTTP " + str(status) + ")")
        sts = boto3.client("sts", endpoint_url=endpoint, aws_access_key_id=key,
                           aws_secret_access_key=secret, region_name="us-east-1",
                           config=Config(connect_timeout=3, read_timeout=10, retries={"max_attempts": 0}))
        try:
            sts.assume_role(RoleArn="arn:aws:iam::000000000000:role/probe", RoleSessionName="probe", DurationSeconds=900)
        except ClientError as error:
            if error.response["Error"]["Code"] != "AccessDenied":
                raise CheckFailure("unexpected STS denial code " + error.response["Error"]["Code"]) from None
        else:
            raise CheckFailure("customer obtained additional STS credentials")
        print("PASS isolation, forbidden bucket administration and additional credentials")

        stage = "presigned and multipart"
        presigned = user.generate_presigned_url("get_object", Params={"Bucket": bucket, "Key": "sample"}, ExpiresIn=300)
        if url_status(presigned) != 200:
            raise CheckFailure("presigned GET failed")
        put_url = user.generate_presigned_url("put_object", Params={"Bucket": bucket, "Key": "signed"}, ExpiresIn=300)
        request = urllib.request.Request(put_url, data=b"signed content", method="PUT")
        with urllib.request.urlopen(request, timeout=10) as response:
            if response.status != 200:
                raise CheckFailure("presigned PUT failed")
        upload = user.create_multipart_upload(Bucket=bucket, Key="multipart")["UploadId"]
        uploads.append((bucket, "multipart", upload))
        part = user.upload_part(Bucket=bucket, Key="multipart", UploadId=upload, PartNumber=1, Body=b"m" * 65536)
        user.complete_multipart_upload(Bucket=bucket, Key="multipart", UploadId=upload,
                                       MultipartUpload={"Parts": [{"PartNumber": 1, "ETag": part["ETag"]}]})
        uploads.clear()
        print("PASS presigned GET/PUT and multipart")

        stage = "quota enforcement"
        try:
            user.put_object(Bucket=bucket, Key="too-large", Body=b"q" * (limit + 1))
        except ClientError as error:
            code = error.response["Error"]["Code"]
            # RustFS 1.0.4 reports quota rejection as InvalidRequest, not an AWS-specific quota code.
            if code != "InvalidRequest" or not error.response["Error"].get("Message", "").startswith("Bucket quota exceeded."):
                raise CheckFailure("unexpected quota rejection code " + code) from None
        else:
            raise CheckFailure("oversized object was accepted")
        if user.list_objects_v2(Bucket=bucket, Prefix="too-large").get("KeyCount", 0) != 0:
            raise CheckFailure("rejected object became visible")
        print("PASS oversized PUT rejected without creating an object")

        stage = "rotation preserving policy and status"
        replacement = secrets.token_hex(32)
        expect_admin(endpoint, root_key, root_secret, "PUT", "set-user-secret-key", {"accessKey": key}, {"secret_key": replacement})
        deny(lambda: user.head_object(Bucket=bucket, Key="sample"))
        if url_status(presigned) != 403:
            raise CheckFailure("old presigned URL was not rejected with HTTP 403")
        user = client(endpoint, key, replacement)
        user.head_object(Bucket=bucket, Key="sample")
        deny(lambda: user.list_objects_v2(Bucket=other))
        print("PASS secret rotation and old URL rejection")

        stage = "suspend and resume"
        expect_admin(endpoint, root_key, root_secret, "PUT", "set-user-status", {"accessKey": key, "status": "disabled"})
        deny(lambda: user.get_object(Bucket=bucket, Key="sample"))
        expect_admin(endpoint, root_key, root_secret, "PUT", "set-user-status", {"accessKey": key, "status": "enabled"})
        user.head_object(Bucket=bucket, Key="sample")
        print("PASS suspend/resume without resending secret")
    except ClientError as error:
        raise CheckFailure(stage + ": S3 " + error.response["Error"]["Code"]) from None
    except CheckFailure as error:
        raise CheckFailure(stage + ": " + str(error)) from None
    finally:
        # Names are generated by this invocation and cleanup is restricted to its creation receipts.
        for name, object_key, upload in uploads:
            try:
                root.abort_multipart_upload(Bucket=name, Key=object_key, UploadId=upload)
            except ClientError:
                pass
        for name in created_buckets:
            try:
                for page in root.get_paginator("list_objects_v2").paginate(Bucket=name):
                    for obj in page.get("Contents", []):
                        root.delete_object(Bucket=name, Key=obj["Key"])
                root.delete_bucket(Bucket=name)
            except ClientError:
                print("WARN fixture bucket cleanup incomplete: " + name)
        if created_user:
            status, _ = admin(endpoint, root_key, root_secret, "DELETE", "remove-user", {"accessKey": key})
            if status != 200:
                print("WARN fixture identity cleanup incomplete")
        if created_policy:
            status, _ = admin(endpoint, root_key, root_secret, "DELETE", "remove-canned-policy", {"name": policy_name})
            if status != 200:
                print("WARN fixture policy cleanup incomplete")


if __name__ == "__main__":
    try:
        run()
    except CheckFailure as error:
        print("FAIL " + str(error))
        sys.exit(1)
    except Exception as error:
        print("FAIL unexpected " + type(error).__name__ + " (details withheld to avoid exposing credentials)")
        sys.exit(1)
