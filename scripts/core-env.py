#!/usr/bin/env python3
"""Generate database and vault keys for the isolated core fixture.
Existing files are retained; no credential value is printed.
"""
import os
from pathlib import Path
import secrets

target = Path(__file__).resolve().parents[1] / ".local" / "core.env"
target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
try:
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    print("Local core environment exists; unchanged.")
else:
    with os.fdopen(fd, "w") as stream:
        stream.write("CORE_TEST_DB_PASSWORD=" + secrets.token_hex(24) + "\n")
        stream.write("CORE_TEST_VAULT_KEY=" + secrets.token_hex(32) + "\n")
    print("Local core environment created (mode 0600); values not displayed.")
