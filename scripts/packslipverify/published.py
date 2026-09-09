#!/usr/bin/env python3
"""Consume a published OIDC-signed tag through mise in the canonical smoke HOME."""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

from process import run, smoke_environment


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r"v\d+\.\d+\.\d+", sys.argv[1]):
        raise RuntimeError("expected explicit vX.Y.Z tag")
    tag = sys.argv[1]
    home = Path(os.environ["HOME"])
    if not home.name.startswith("kae-smoke-run."):
        raise RuntimeError("run through the published release verifier's smoke harness")
    mise = shutil.which("mise")
    if not mise:
        raise RuntimeError("mise is required for published consumer verification")
    env = smoke_environment()
    version = run([mise, "--version"], home, env).strip()
    if not version.startswith("2026.9.3 "):
        raise RuntimeError("published consumer verification requires tested mise 2026.9.3")
    tool = "packslip:github.com/webkaz-labs/kagikae"
    identity = "https://github.com/webkaz-labs/kagikae/.github/workflows/release.yml@refs/tags/" + tag
    config = Path(env["XDG_CONFIG_HOME"]) / "mise/config.toml"
    config.parent.mkdir(parents=True, exist_ok=True)
    # The fresh-release exception is opt-in and changes only this smoke HOME.
    fresh = os.environ.get("KAE_RELEASE_VERIFY_FRESH") == "1"
    settings = '[settings]\nminimum_release_age = "0"\n' if fresh else ""
    # No fixture key, unlogged override or URL replacement.
    config.write_text(settings + "[tools]\n" + json.dumps(tool) + " = { version = " + json.dumps(tag[1:]) +
                      ", identity = " + json.dumps(identity) + ', issuer = "https://token.actions.githubusercontent.com" }\n')
    run([mise, "install"], home, env)
    installed = Path(run([mise, "where", tool], home, env).strip())
    reported = run([mise, "exec", "--", "kae", "version"], home, env).strip()
    if reported != "kae " + tag:
        raise RuntimeError("published consumer selected the wrong version")
    for shell in ("bash", "zsh", "fish"):
        completion = run([mise, "completion", shell, "--tool", "kae"], home, env)
        if completion != (installed / f"completions/kae.{shell}").read_text():
            raise RuntimeError("published completion resource mismatch: " + shell)
    if "uninstall" not in run([mise, "exec", "--", "kae", "__complete", "commands"], home, env).splitlines():
        raise RuntimeError("published dynamic command completion is stale")
    print(json.dumps({"status": "success", "tag": tag, "mise": version, "native_version": reported,
                      "trust": "GitHub OIDC exact workflow/tag", "release_age": "explicit isolated zero-age exception" if fresh else "default policy"}))


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.TimeoutExpired) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
