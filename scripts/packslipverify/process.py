"""Bounded subprocesses owned by a release smoke, with no inherited auth env."""

import os
import signal
import subprocess


def smoke_environment():
    allowed = {"PATH", "HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "MISE_CEILING_PATHS", "GOCACHE", "GOMODCACHE", "GOPATH", "KAE_CLAUDE_DRIVER"}
    return {k: v for k, v in os.environ.items() if k in allowed} | {"NO_COLOR": "1", "MISE_YES": "1"}


def run(args, cwd, env, expected=0, input_text=None):
    child = subprocess.Popen([str(a) for a in args], cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = child.communicate(input=input_text, timeout=120)
    finally:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait(timeout=5)
    if (expected == 0 and child.returncode != 0) or (expected != 0 and child.returncode == 0):
        raise RuntimeError(f"{args[0]} {args[1:]}: exit {child.returncode}\n{stdout}\n{stderr}")
    return stdout if expected == 0 else stdout + stderr
