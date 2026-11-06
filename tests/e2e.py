#!/usr/bin/env python3
"""Acceptance through an unchanged core, the real Go agent and the S3 gateway.
All credentials are generated or read from the isolated fixture; logs stay in memory.
"""
import concurrent.futures
import datetime
import io
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from botocore.exceptions import ClientError
from backend_contract import CheckFailure, admin, client, deny, expect_admin, fixture_credentials, url_status, require_fixtures

ROOT = Path(__file__).resolve().parents[1]
CORE = "http://127.0.0.1:18000/api/v1"
BACKEND = "http://127.0.0.1:" + str(int(os.environ.get("RUSTFS_TEST_PORT", "19000")))
CUSTOMER = "http://127.0.0.1:" + str(int(os.environ.get("S3_CLIENT_PORT", "19080")))
REDACT = []


def scrub(text):
    for secret in REDACT:
        if secret:
            text = text.replace(secret, "[redacted]")
    return text


def new_secret():
    value = secrets.token_hex(32)
    REDACT.append(value)
    return value


class Core:
    def __init__(self, token="change-me"):
        # The unchanged core seeds this well-known bootstrap value. The fixture is loopback-only.
        self.token = token

    def request(self, method, path, body=None, expected=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(CORE + path, data=data, method=method,
                                     headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
            error.close()
        if expected is not None:
            if status != expected:
                raise CheckFailure("core " + method + " " + path.split("?")[0] + " returned HTTP " + str(status))
        elif not 200 <= status < 300:
            raise CheckFailure("core " + method + " " + path.split("?")[0] + " HTTP " + str(status) + ": " + scrub(raw.decode())[:800])
        return json.loads(raw) if raw else None

    def all(self, resource, **filters):
        result = []
        for page in range(1, 100):
            query = urllib.parse.urlencode({**filters, "page": page, "pageSize": 100})
            data = self.request("GET", "/" + resource + "?" + query)
            result.extend(data["items"])
            if page >= data["totalPages"]:
                return result
        raise CheckFailure("core pagination did not terminate")

    def job_ids(self, service_id):
        return {job["id"] for job in self.all("jobs", serviceId=service_id)}

    def wait_job(self, service_id, previous=(), expected="Completed", timeout=75):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            jobs = [job for job in self.all("jobs", serviceId=service_id) if job["id"] not in previous]
            if jobs:
                job = max(jobs, key=lambda item: item["createdAt"])
                if job["status"] in ("Completed", "Failed"):
                    if job["status"] != expected:
                        raise CheckFailure("job " + job["action"] + " was " + job["status"] + ": " + scrub(job.get("errorMessage", "")))
                    return job
            time.sleep(0.1)
        raise CheckFailure("job did not finish within the acceptance timeout")

    def action(self, service_id, action, expected="Completed"):
        previous = self.job_ids(service_id)
        if action == "delete":
            self.request("DELETE", "/services/" + service_id)
        else:
            self.request("POST", "/services/" + service_id + "/" + action, {})
        return self.wait_job(service_id, previous, expected)

    def update(self, service_id, properties, expected="Completed"):
        previous = self.job_ids(service_id)
        self.request("PATCH", "/services/" + service_id, {"properties": properties})
        return self.wait_job(service_id, previous, expected)


def assert_quota_rejection(operation):
    try:
        operation()
    except ClientError as error:
        info = error.response.get("Error", {})
        if info.get("Code") != "InvalidRequest" or not info.get("Message", "").startswith("Bucket quota exceeded."):
            raise CheckFailure("unexpected error instead of quota rejection: " + info.get("Code", "unknown")) from None
    else:
        raise CheckFailure("quota was bypassed")


def object_bytes(s3, bucket, key, **kwargs):
    response = s3.get_object(Bucket=bucket, Key=key, **kwargs)
    try:
        return response["Body"].read()
    finally:
        response["Body"].close()


def empty_objects(s3, bucket):
    for page in s3.get_paginator("list_objects_v2").paginate(Bucket=bucket):
        for obj in page.get("Contents", []):
            s3.delete_object(Bucket=bucket, Key=obj["Key"])


def run():
    require_fixtures(need_core=True)
    root_key, root_secret = fixture_credentials()
    REDACT.extend((root_key, root_secret))
    storage = client(BACKEND, root_key, root_secret)
    core = Core()
    run_id = uuid.uuid4().hex[:12]
    provider = core.request("POST", "/participants", {"name": "s3-provider-" + run_id, "status": "Enabled"})
    consumer = core.request("POST", "/participants", {"name": "s3-consumer-" + run_id, "status": "Enabled"})
    group = core.request("POST", "/service-groups", {"name": "s3-group-" + run_id, "consumerId": consumer["id"]})
    service_doc = json.loads((ROOT / "examples/service-type.json").read_text())
    service_doc["name"] += "-" + run_id
    service_type = core.request("POST", "/service-types", service_doc)
    agent_doc = json.loads((ROOT / "examples/agent-type.json").read_text())
    agent_doc["name"] += "-" + run_id
    agent_doc["serviceTypeIds"] = [service_type["id"]]
    agent_type = core.request("POST", "/agent-types", agent_doc)
    for metric in json.loads((ROOT / "examples/metric-types.json").read_text()):
        matches = [item for item in core.all("metric-types", name=metric["name"]) if item["name"] == metric["name"]]
        if not matches:
            core.request("POST", "/metric-types", metric)
        elif matches[0]["entityType"] != metric["entityType"]:
            raise CheckFailure("existing metric type has an incompatible entity type")
    record = core.request("POST", "/agents", {
        "name": "s3-agent-" + run_id, "providerId": provider["id"], "agentTypeId": agent_type["id"],
        "configuration": {"rustfs": {"endpoint": BACKEND, "clientEndpoint": CUSTOMER, "region": "us-east-1",
                                    "accessKey": root_key, "secretKey": root_secret,
                                    "httpTimeoutSeconds": 15, "operationTimeoutSeconds": 120}},
    })
    if root_secret in json.dumps(record) or root_key in json.dumps(record):
        raise CheckFailure("core exposed administrative credentials instead of vault references")
    changed_backend = json.loads(json.dumps(record["configuration"]))
    changed_backend["rustfs"]["endpoint"] = "http://127.0.0.1:19999"
    core.request("PATCH", "/agents/" + record["id"], {"configuration": changed_backend}, expected=400)
    token = core.request("POST", "/tokens", {"name": "s3-agent-" + run_id, "role": "agent", "scopeId": record["id"]})
    agent_token = token["value"]
    REDACT.append(agent_token)
    agent_core = Core(agent_token)
    env = os.environ.copy()
    env.update({"S3_AGENT_FULCRUM_API_URL": CORE.removesuffix("/api/v1"), "S3_AGENT_FULCRUM_API_TOKEN": agent_token,
                "S3_AGENT_JOB_POLL_INTERVAL": "200ms", "S3_AGENT_METRIC_REPORT_INTERVAL": "1s",
                "S3_AGENT_HEALTH_INTERVAL": "2s"})
    output = io.BytesIO()
    def start_agent():
        child = subprocess.Popen([str(ROOT / ".local/agent")], cwd=ROOT, env=env,
                                 stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        def read_logs():
            for line in iter(child.stdout.readline, b""):
                output.write(line)
        thread = threading.Thread(target=read_logs, daemon=True)
        thread.start()
        return child, thread

    def stop_agent(child, thread):
        if child.poll() is None:
            child.send_signal(signal.SIGTERM)
        try:
            child.wait(timeout=35)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()
        thread.join(timeout=5)
        child.stdout.close()
        return child.returncode

    def wait_agent_status(expected):
        for _ in range(150):
            if core.request("GET", "/agents/" + record["id"])["status"] == expected:
                return
            time.sleep(0.2)
        raise CheckFailure("agent did not reach " + expected)

    process, reader = start_agent()
    services = []
    succeeded = False
    try:
        for _ in range(150):
            current = core.request("GET", "/agents/" + record["id"])
            if current["status"] == "Connected":
                break
            if process.poll() is not None:
                raise CheckFailure("agent exited during startup; raw logs withheld")
            time.sleep(0.2)
        else:
            raise CheckFailure("agent did not connect")
        print("PASS real agent connection, remote configuration and vault resolution")

        def create_service(name, quota):
            secret = new_secret()
            svc = core.request("POST", "/services", {"name": name, "agentId": record["id"],
                               "serviceTypeId": service_type["id"], "groupId": group["id"],
                               "properties": {"quotaBytes": quota, "secretAccessKey": secret}})
            services.append(svc["id"])
            reference = svc["properties"]["secretAccessKey"]
            if not reference.startswith("vault://"):
                raise CheckFailure("customer secret was not vaulted")
            core.wait_job(svc["id"])
            result = core.request("GET", "/services/" + svc["id"])
            if result["status"] != "Active" or secret in json.dumps(result):
                raise CheckFailure("invalid created service state or secret exposure")
            agent_core.request("GET", "/vault/secrets/" + reference.removeprefix("vault://"), expected=404)
            data = result["agentInstanceData"]
            if data["endpoint"] != CUSTOMER or data["bucket"] != "fulcrum-" + svc["id"]:
                raise CheckFailure("invalid advertised resources")
            return svc["id"], data, secret, client(CUSTOMER, data["accessKeyId"], secret, attempts=4)

        sid_a, data_a, secret_a, a = create_service("s3-a-" + run_id, 16 * 1024 * 1024)
        sid_b, data_b, secret_b, b = create_service("s3-b-" + run_id, 1024 * 1024)
        bucket_a, bucket_b = data_a["bucket"], data_b["bucket"]
        print("PASS two isolated services, secret-free runtime and ephemeral cleanup")

        a.put_object(Bucket=bucket_a, Key="sample", Body=b"sample")
        response = a.get_object(Bucket=bucket_a, Key="sample")
        if response["Body"].read() != b"sample":
            raise CheckFailure("object data mismatch")
        response["Body"].close()
        deny(lambda: a.get_object(Bucket=bucket_b, Key="sample"))
        deny(lambda: a.put_object(Bucket=bucket_b, Key="foreign", Body=b"no"))
        if url_status(CUSTOMER + "/" + bucket_a + "/sample") != 403:
            raise CheckFailure("anonymous object access allowed")
        for key in ("space name", "unicode-ü/%plus+", "folder/key"):
            a.put_object(Bucket=bucket_a, Key=key, Body=b"encoded")
            a.head_object(Bucket=bucket_a, Key=key)
        core.request("PATCH", "/services/" + sid_a, {"name": "renamed-" + run_id})
        if core.request("GET", "/services/" + sid_a)["agentInstanceData"]["bucket"] != bucket_a:
            raise CheckFailure("display-name change altered the technical bucket")
        print("PASS data isolation, private access, stable names and encoded keys through the gateway")

        old_url = a.generate_presigned_url("get_object", Params={"Bucket": bucket_a, "Key": "sample"}, ExpiresIn=300)
        if url_status(old_url) != 200:
            raise CheckFailure("presigned GET failed")
        put_url = a.generate_presigned_url("put_object", Params={"Bucket": bucket_a, "Key": "signed"}, ExpiresIn=300)
        with urllib.request.urlopen(urllib.request.Request(put_url, data=b"presigned", method="PUT"), timeout=10) as response:
            if response.status != 200:
                raise CheckFailure("presigned PUT failed")
        if object_bytes(a, bucket_a, "signed") != b"presigned":
            raise CheckFailure("presigned PUT changed the content")
        upload = a.create_multipart_upload(Bucket=bucket_a, Key="multipart")["UploadId"]
        parts = []
        for number, size in ((1, 5 * 1024 * 1024), (2, 1024 * 1024)):
            part = a.upload_part(Bucket=bucket_a, Key="multipart", UploadId=upload, PartNumber=number, Body=bytes([number]) * size)
            parts.append({"PartNumber": number, "ETag": part["ETag"]})
        a.complete_multipart_upload(Bucket=bucket_a, Key="multipart", UploadId=upload, MultipartUpload={"Parts": parts})
        if object_bytes(a, bucket_a, "multipart") != b"\x01" * (5 * 1024 * 1024) + b"\x02" * (1024 * 1024):
            raise CheckFailure("multipart content or part ordering mismatch")
        print("PASS presigned GET/PUT and multi-part large upload")

        # CLI credentials are passed only via this child environment, never argv or a file.
        payload = ROOT / ".local" / ("cli-payload-" + run_id)
        payload.write_bytes(b"synthetic AWS CLI acceptance data")
        cli_env = {key: value for key, value in os.environ.items() if not key.startswith("AWS_")}
        cli_env.update({"AWS_ACCESS_KEY_ID": data_a["accessKeyId"], "AWS_SECRET_ACCESS_KEY": secret_a,
                        "AWS_EC2_METADATA_DISABLED": "true", "AWS_CONFIG_FILE": os.devnull,
                        "AWS_SHARED_CREDENTIALS_FILE": os.devnull})
        try:
            cli = subprocess.run([str(ROOT / ".local/venv/bin/aws"), "--endpoint-url", CUSTOMER, "--region", "us-east-1",
                                  "s3api", "put-object", "--bucket", bucket_a, "--key", "cli", "--body", str(payload)],
                                 env=cli_env, capture_output=True, timeout=30)
            if cli.returncode:
                raise CheckFailure("AWS CLI upload failed; output withheld")
            if object_bytes(a, bucket_a, "cli") != payload.read_bytes():
                raise CheckFailure("AWS CLI uploaded different content")
        finally:
            payload.unlink(missing_ok=True)
        print("PASS AWS CLI interoperability")

        assert_quota_rejection(lambda: b.put_object(Bucket=bucket_b, Key="oversized", Body=b"x" * (1024 * 1024 + 1)))
        b.put_object(Bucket=bucket_b, Key="overwrite", Body=b"o" * 400000)
        b.put_object(Bucket=bucket_b, Key="overwrite", Body=b"n" * 500000)
        assert_quota_rejection(lambda: b.put_object(Bucket=bucket_b, Key="overwrite", Body=b"x" * 2000000))
        if object_bytes(b, bucket_b, "overwrite") != b"n" * 500000:
            raise CheckFailure("rejected overwrite changed the existing object")
        b.copy_object(Bucket=bucket_b, Key="copy", CopySource={"Bucket": bucket_b, "Key": "overwrite"})
        assert_quota_rejection(lambda: b.copy_object(Bucket=bucket_b, Key="excess-copy", CopySource={"Bucket": bucket_b, "Key": "overwrite"}))
        if object_bytes(b, bucket_b, "copy") != b"n" * 500000 or b.list_objects_v2(Bucket=bucket_b, Prefix="excess-copy").get("KeyCount", 0):
            raise CheckFailure("copy content or over-quota visibility mismatch")
        empty_objects(b, bucket_b)
        print("PASS quota on ordinary PUT, overwrite and copy")

        def put_concurrently(number):
            try:
                b.put_object(Bucket=bucket_b, Key="parallel-" + str(number), Body=b"p" * 262144)
                return True
            except ClientError as error:
                if error.response.get("Error", {}).get("Code") != "InvalidRequest" or not error.response["Error"].get("Message", "").startswith("Bucket quota exceeded."):
                    raise CheckFailure("unexpected concurrent upload failure") from None
                return False
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            successes = sum(executor.map(put_concurrently, range(8)))
        used = sum(obj["Size"] for page in b.get_paginator("list_objects_v2").paginate(Bucket=bucket_b) for obj in page.get("Contents", []))
        if successes == 0 or used > 1024 * 1024 or used != successes * 262144:
            raise CheckFailure("concurrent writes exceeded quota or produced inconsistent data")
        empty_objects(b, bucket_b)
        print("PASS concurrent quota enforcement")

        upload = b.create_multipart_upload(Bucket=bucket_b, Key="too-large-multipart")["UploadId"]
        try:
            def oversized_multipart():
                part = b.upload_part(Bucket=bucket_b, Key="too-large-multipart", UploadId=upload, PartNumber=1, Body=b"z" * 2000000)
                b.complete_multipart_upload(Bucket=bucket_b, Key="too-large-multipart", UploadId=upload,
                                            MultipartUpload={"Parts": [{"PartNumber": 1, "ETag": part["ETag"]}]})
            assert_quota_rejection(oversized_multipart)
            if b.list_objects_v2(Bucket=bucket_b, Prefix="too-large-multipart").get("KeyCount", 0):
                raise CheckFailure("over-quota multipart object became visible")
        finally:
            b.abort_multipart_upload(Bucket=bucket_b, Key="too-large-multipart", UploadId=upload)
        print("PASS multipart quota rejection without visible object")

        core.update(sid_b, {"quotaBytes": 2 * 1024 * 1024})
        core.update(sid_b, {"quotaBytes": 1024 * 1024}, expected="Failed")
        quota = expect_admin(BACKEND, root_key, root_secret, "GET", "get-bucket-quota", {"bucket": bucket_b})
        if quota["quota"] != 2 * 1024 * 1024:
            raise CheckFailure("rejected quota reduction changed the backend")
        # Core keeps attempted update properties on an error transition. Reconcile explicitly.
        core.update(sid_b, {"quotaBytes": 2 * 1024 * 1024})
        replacement = new_secret()
        core.update(sid_a, {"secretAccessKey": replacement})
        deny(lambda: a.get_object(Bucket=bucket_a, Key="sample"))
        if url_status(old_url) != 403:
            raise CheckFailure("old presigned URL survived rotation")
        a = client(CUSTOMER, data_a["accessKeyId"], replacement, attempts=4)
        a.head_object(Bucket=bucket_a, Key="sample")
        core.action(sid_a, "suspend")
        deny(lambda: a.get_object(Bucket=bucket_a, Key="sample"))
        suspended_replacement = new_secret()
        core.update(sid_a, {"secretAccessKey": suspended_replacement})
        a = client(CUSTOMER, data_a["accessKeyId"], suspended_replacement, attempts=4)
        deny(lambda: a.get_object(Bucket=bucket_a, Key="sample"))
        if stop_agent(process, reader) != 0:
            raise CheckFailure("agent did not shut down cleanly before restart")
        process, reader = start_agent()
        wait_agent_status("Connected")
        deny(lambda: a.get_object(Bucket=bucket_a, Key="sample"))
        core.action(sid_a, "resume")
        a.head_object(Bucket=bucket_a, Key="sample")
        print("PASS quota updates, rotation, agent restart preserving suspension and resume")

        for _ in range(30):
            entries = core.all("metric-entries", serviceId=sid_a)
            names = {(entry.get("type") or {}).get("name") for entry in entries}
            if {"s3.storage.used_bytes", "s3.storage.quota_bytes", "s3.storage.usage_ratio"} <= names:
                break
            time.sleep(0.5)
        else:
            raise CheckFailure("expected metrics were not delivered to the real core")
        print("PASS metrics delivered through stdagent to the core")

        # Exercise mutation-safe deletion over more than one S3 listing page.
        def pagination_object(number):
            a.put_object(Bucket=bucket_a, Key="page/" + str(number).zfill(5), Body=b"")
        # This checks pagination, not throughput. Use the standard bounded SDK retry policy.
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
            list(executor.map(pagination_object, range(1005)))
        pages = list(a.get_paginator("list_objects_v2").paginate(Bucket=bucket_a, Prefix="page/", PaginationConfig={"PageSize": 1000}))
        if len(pages) < 2 or sum(len(page.get("Contents", [])) for page in pages) != 1005:
            raise CheckFailure("force-delete fixture did not span multiple complete pages")
        print("PASS populated and verified a multi-page force-delete fixture")

        # External versioning is outside customer permissions, but force delete must remove its remnants.
        storage.put_bucket_versioning(Bucket=bucket_a, VersioningConfiguration={"Status": "Enabled"})
        a.put_object(Bucket=bucket_a, Key="versioned", Body=b"one")
        a.put_object(Bucket=bucket_a, Key="versioned", Body=b"two")
        a.delete_object(Bucket=bucket_a, Key="versioned")
        pending = a.create_multipart_upload(Bucket=bucket_a, Key="unfinished")["UploadId"]
        a.upload_part(Bucket=bucket_a, Key="unfinished", UploadId=pending, PartNumber=1, Body=b"unfinished")
        core.action(sid_a, "delete")
        core.action(sid_b, "delete")
        for sid, data in ((sid_a, data_a), (sid_b, data_b)):
            if core.request("GET", "/services/" + sid)["status"] != "Deleted":
                raise CheckFailure("core did not enter terminal state")
            try:
                storage.head_bucket(Bucket=data["bucket"])
            except ClientError as error:
                if error.response["ResponseMetadata"]["HTTPStatusCode"] != 404:
                    raise CheckFailure("unexpected missing-bucket response") from None
            else:
                raise CheckFailure("bucket survived force deletion")
            status, _ = admin(BACKEND, root_key, root_secret, "GET", "user-info", {"accessKey": data["accessKeyId"]})
            if status != 404:
                raise CheckFailure("credential survived force deletion")
            policy = "fc-" + sid.replace("-", "")
            status, _ = admin(BACKEND, root_key, root_secret, "GET", "info-canned-policy", {"name": policy})
            if status != 404:
                raise CheckFailure("policy survived force deletion")
        print("PASS multi-page force delete of versioned buckets and unfinished multipart; no IAM orphans")

        sid_c, data_c, secret_c, c = create_service("s3-held-" + run_id, 4 * 1024 * 1024)
        bucket_c = data_c["bucket"]
        storage.put_bucket_versioning(Bucket=bucket_c, VersioningConfiguration={"Status": "Enabled"})
        storage.put_object_lock_configuration(Bucket=bucket_c, ObjectLockConfiguration={"ObjectLockEnabled": "Enabled"})
        storage.put_object(Bucket=bucket_c, Key="a-removable", Body=b"removable")
        held = storage.put_object(Bucket=bucket_c, Key="z-held", Body=b"held", ObjectLockLegalHoldStatus="ON")
        version = held["VersionId"]
        try:
            core.action(sid_c, "delete", expected="Failed")
            if core.request("GET", "/services/" + sid_c)["status"] != "DeleteFailed":
                raise CheckFailure("partial deletion did not enter DeleteFailed")
            compose = ["docker", "compose", "--project-name", "fulcrum-s3-test", "--env-file", str(ROOT / ".local/rustfs.env"),
                       "-f", str(ROOT / "compose.yaml")]
            stopped = subprocess.run(compose + ["stop", "rustfs"], capture_output=True, timeout=40)
            if stopped.returncode:
                raise CheckFailure("cannot stop the dedicated backend fixture")
            try:
                wait_agent_status("Error")
                previous = core.job_ids(sid_c)
                core.request("DELETE", "/services/" + sid_c)
                time.sleep(0.5)
                queued = [job for job in core.all("jobs", serviceId=sid_c) if job["id"] not in previous]
                if len(queued) != 1 or queued[0]["status"] != "Pending":
                    raise CheckFailure("job progressed while the backend was unavailable")
            finally:
                started = subprocess.run(compose + ["start", "rustfs"], capture_output=True, timeout=40)
                if started.returncode:
                    raise CheckFailure("cannot restart the dedicated backend fixture")
            wait_agent_status("Connected")
            core.wait_job(sid_c, previous, expected="Failed")  # The hold must still be enforced after restart.
            tags = {tag["Key"]: tag["Value"] for tag in storage.get_bucket_tagging(Bucket=bucket_c)["TagSet"]}
            if tags.get("fulcrum-phase") != "deleting":
                raise CheckFailure("durable deletion barrier missing")
            deny(lambda: c.get_object(Bucket=bucket_c, Key="z-held"))
            core.request("POST", "/services/" + sid_c + "/resume", {}, expected=400)
            if storage.list_objects_v2(Bucket=bucket_c, Prefix="a-removable").get("KeyCount", 0):
                raise CheckFailure("fixture did not exercise a partial deletion")
            if object_bytes(storage, bucket_c, "z-held", VersionId=version) != b"held":
                raise CheckFailure("held object did not survive the backend restart")
            if storage.get_object_legal_hold(Bucket=bucket_c, Key="z-held", VersionId=version)["LegalHold"]["Status"] != "ON":
                raise CheckFailure("agent overrode the hold")
        finally:
            # Release only the synthetic hold installed by this test, never bypass it in the agent.
            storage.put_object_legal_hold(Bucket=bucket_c, Key="z-held", VersionId=version, LegalHold={"Status": "OFF"})
        core.action(sid_c, "delete")
        if core.request("GET", "/services/" + sid_c)["status"] != "Deleted":
            raise CheckFailure("explicit delete retry failed")
        print("PASS backend outage/recovery, persistent deletion barrier and hold, explicit retry after operator release")
        succeeded = True
    finally:
        exit_code = stop_agent(process, reader)
        logs = output.getvalue()
        for secret in REDACT:
            if secret.encode() in logs:
                raise CheckFailure("secret appeared in agent logs (logs not displayed)")
        for line in logs.splitlines():
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if "config" in entry or "configuration" in entry:
                raise CheckFailure("resolved configuration dump appeared in agent logs")
        core.request("DELETE", "/tokens/" + token["id"])
        if exit_code != 0 or b"WARNING: DATA RACE" in logs:
            raise CheckFailure("agent exited abnormally or reported a data race; logs withheld")
        if succeeded:
            print("PASS graceful shutdown and secret-free agent logs")
        else:
            print("INFO stopped only the test agent; inspect fixture services by IDs: " + ", ".join(services))


if __name__ == "__main__":
    try:
        run()
    except CheckFailure as error:
        print("FAIL " + scrub(str(error)))
        sys.exit(1)
    except ClientError as error:
        print("FAIL unexpected S3 " + error.response.get("Error", {}).get("Code", "unknown"))
        sys.exit(1)
    except Exception as error:
        print("FAIL unexpected " + type(error).__name__ + " (details withheld)")
        sys.exit(1)
