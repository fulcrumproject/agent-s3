#!/usr/bin/env python3
"""Generate credentials only for the isolated Docker fixture.
Never print secrets or overwrite an existing environment file.
"""
import os
import pathlib
import secrets

target = pathlib.Path(__file__).resolve().parents[1] / ".local" / "rustfs.env"
target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
try:
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    print("Local test environment already exists; unchanged.")
else:
    with os.fdopen(fd, "w") as stream:
        stream.write("RUSTFS_ACCESS_KEY=" + secrets.token_hex(10) + "\n")
        stream.write("RUSTFS_SECRET_KEY=" + secrets.token_hex(32) + "\n")
    print("Local test environment created (mode 0600); values not displayed.")
