#!/usr/bin/env python3
"""Login-free Claude naming agreement. Run from the repository via mise.

Only the previously inspected artifact may execute: updating the digest requires
the upstream-auth-drift source/PATH-reachability review, not a version string.
The shim never forwards to security. This is not an OS network sandbox, nor proof
about other upstream builds, direct keychain APIs, or the CLI bind environment.
"""
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

# Claude 2.1.261, measured 2026-09-06: eleven non-forwarding shim cases
# reached both service families; resolver source at byte offsets 157959463 and
# 156723208. Re-establish reachability using the repo-local upstream-auth-drift
# skill's references/measuring.md before replacing this allowlisted digest.
REVIEWED_SHA256 = "5efecaff231b798be3c66def9be54183623b328b80eaef17f93c43987024e82a"


class Refused(RuntimeError):
    pass


def verified_copy(source, target):
    if not source or not Path(source).is_file():
        raise Refused("Claude unavailable; no upstream invocation")
    shutil.copyfile(source, target)
    with target.open("rb") as binary:
        digest = hashlib.file_digest(binary, "sha256").hexdigest()
    if digest != REVIEWED_SHA256:
        raise Refused("unreviewed Claude bytes; establish PATH reachability before updating the digest")
    target.chmod(0o700)


@contextmanager
def isolated_env(repo):
    # Only this independently owned parent is ever recursively removed, even if
    # the sourced preamble fails or returns a malformed HOME.
    with tempfile.TemporaryDirectory(prefix="kae-naming-") as owned:
        p = subprocess.run(["/bin/sh", "-eu", "-c", 'mktemp() { /usr/bin/mktemp -d "$NAMING_PARENT/home.XXXXXXXX"; }; . "$1"; env -0', "sh", str(repo / "scripts/smoke-env.sh")],
                           env={"PATH": "/usr/bin:/bin", "TMPDIR": owned, "NAMING_PARENT": owned}, check=True, capture_output=True)
        env = dict(item.decode().split("=", 1) for item in p.stdout.split(b"\0") if item)
        home = Path(env.get("HOME", ""))
        if not home.is_absolute() or not home.is_dir() or home.resolve().parent != Path(owned).resolve():
            raise Refused("isolation preamble did not create an owned HOME")
        yield env


def install_shim(root, env):
    shim = root / "shim"
    shim.mkdir()
    program = shim / "security"
    program.write_text("#!" + sys.executable + "\nimport json,os,sys\n"
                       "with open(os.environ['NAMING_LOG'],'a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\n"
                       "sys.exit(44)\n")
    program.chmod(0o700)
    env.update(PATH=str(shim) + ":/usr/bin:/bin", NAMING_LOG=str(root / "security.jsonl"))
    preflight(env, program)


def preflight(env, program):
    if not env.get("HOME") or not Path(env["HOME"]).is_absolute():
        raise Refused("isolation HOME unavailable")
    home = Path(env["HOME"]).resolve()
    for key in ("XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"):
        if not env.get(key) or not Path(env[key]).resolve().is_relative_to(home):
            raise Refused("isolation root escaped: " + key)
    if shutil.which("security", path=env["PATH"]) != str(program) or not program.is_file():
        raise Refused("security shim unavailable")
    log = Path(env["NAMING_LOG"])
    log.write_text("")
    result = subprocess.run(["security", "naming-preflight"], env=env, capture_output=True)
    if result.returncode != 44 or read_log(log) != [["naming-preflight"]]:
        raise Refused("security shim did not intercept preflight")
    log.write_text("")


def read_log(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


def identity(argv):
    try:
        return argv[argv.index("-s") + 1], argv[argv.index("-a") + 1]
    except (ValueError, IndexError) as error:
        raise Refused("unavailable service/account observation") from error


def compare(write, reads):
    if not write or write[0] != "add-generic-password":
        raise Refused("unavailable production write")
    if any(not row or row[0] != "find-generic-password" for row in reads):
        raise Refused("unexpected upstream security operation")
    observed = [identity(row) for row in reads]
    if not observed:
        raise Refused("unavailable upstream read; empty log is not containment proof")
    if identity(write) not in observed:
        raise Refused("credential service/account mismatch")


def verify():
    repo = Path(__file__).resolve().parents[2]
    with isolated_env(repo) as env:
        root = Path(env["HOME"])
        env.update(TMPDIR=str(root / "tmp"), USER="main", DISABLE_AUTOUPDATER="1",
                   CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1")
        Path(env["TMPDIR"]).mkdir()
        upstream = root / "claude"
        verified_copy(shutil.which("claude"), upstream)
        install_shim(root, env)
        observer = root / "observer"
        # Build in the caller's development environment; run only in isolation.
        subprocess.run(["go", "build", "-buildvcs=false", "-o", str(observer), "./scripts/namingagreement"], cwd=repo, check=True)
        config = str(root / "config")
        cases = [("default", {}), ("config", {"CLAUDE_CONFIG_DIR": config}),
                 ("trailing", {"CLAUDE_CONFIG_DIR": config + "/"}),
                 ("unicode", {"CLAUDE_CONFIG_DIR": str(root / "cafe\u0301")}),
                 ("secure", {"CLAUDE_CONFIG_DIR": config, "CLAUDE_SECURESTORAGE_CONFIG_DIR": str(root / "credential")}),
                 ("relative", {"CLAUDE_CONFIG_DIR": "relative"}),
                 ("invalid_user", {"CLAUDE_CONFIG_DIR": config, "USER": "invalid user"})]
        for name, extra in cases:
            case_env = env | extra
            cwd = root / name
            cwd.mkdir()
            preflight(case_env, root / "shim/security")
            write = subprocess.run([str(observer)], env=case_env, cwd=cwd, check=True, capture_output=True)
            result = subprocess.run([str(upstream), "-p", "hi"], env=case_env, cwd=cwd,
                                    stdin=subprocess.DEVNULL, capture_output=True, timeout=45)
            if result.returncode == 0:
                raise Refused("unexpected authenticated upstream success")
            compare(json.loads(write.stdout), read_log(Path(env["NAMING_LOG"])))
            if list(root.rglob(".credentials.json")):
                raise Refused("unexpected plaintext credential fallback")
            print(json.dumps({"case": name, "status": "matched"}), flush=True)


if __name__ == "__main__":
    try:
        verify()
    except (Refused, OSError, subprocess.SubprocessError) as error:
        print("naming agreement failed: " + str(error), file=sys.stderr)
        sys.exit(1)
