#!/usr/bin/env python3
"""Real mise backend fixtures; only run through the Packslip consumer smoke.

Fixture versions are built from a temporary copy with a changed version constant.
The ephemeral signing key and allow_unlogged/age settings belong only to the smoke
HOME. All mise HTTP URLs are redirected to a non-forwarding loopback server.
"""

import http.server
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import urllib.parse

from process import run as run_process, smoke_environment

PROJECT = "github.com/webkaz-labs/kagikae"
TOOL = "packslip:" + PROJECT
COMMIT = "a" * 40


def main():
    home = Path(os.environ["HOME"])
    if not home.name.startswith("kae-smoke-run."):
        raise RuntimeError("run via scripts/smoke-run.sh '## Packslip consumer smoke'")
    repo = Path.cwd()
    mise = shutil.which("mise")
    go = shutil.which("go")
    packslip = os.environ.get("PACKSLIP_BIN") or shutil.which("packslip")
    if not mise or not go or not packslip:
        raise RuntimeError("mise 2026.9.3, Go and verified Packslip 1.1.1 are required")
    env = smoke_environment() | {"GOPROXY": "off"}

    def run(args, cwd, expected=0, extra=None, input_text=None):
        return run_process(args, cwd, env | (extra or {}), expected, input_text)

    if not run([mise, "--version"], home).startswith("2026.9.3 "):
        raise RuntimeError("fixture consumer version must be mise 2026.9.3")
    if run([packslip, "--version"], home).strip() != "packslip 1.1.1":
        raise RuntimeError("fixture signing CLI must be Packslip 1.1.1")
    results = []
    with tempfile.TemporaryDirectory(prefix="packslip-fixtures-", dir=home) as allocated:
        root = Path(allocated)
        source = root / "source"
        source.mkdir()
        for name in ("go.mod", "go.sum", "main.go"):
            shutil.copy2(repo / name, source / name)
        shutil.copytree(repo / "internal", source / "internal")
        original = (source / "internal/cmd/cmd.go").read_text()
        key = root / "fixture.key"
        run([packslip, "keygen", "--out", key], root)
        public_key = key.with_suffix(".pub").read_text().splitlines()[1]
        other_key = root / "other.key"
        run([packslip, "keygen", "--out", other_key], root)
        releases = {}
        files = {}
        requests = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                path = urllib.parse.urlsplit(self.path).path
                requests.append(path)
                if "Authorization" in self.headers:
                    self.send_error(400, "fixture received an authorization header")
                    return
                prefix = "/api.github.com/repos/webkaz-labs/kagikae/releases"
                if path == prefix:
                    payload = json.dumps(list(releases.values())).encode()
                elif path.startswith(prefix + "/tags/"):
                    release = releases.get(path.removeprefix(prefix + "/tags/"))
                    if release is None:
                        self.send_error(404)
                        return
                    payload = json.dumps(release).encode()
                elif path in files:
                    payload = files[path].read_bytes()
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def log_message(self, *_):
                pass

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            global_dir = Path(env["XDG_CONFIG_HOME"]) / "mise"
            global_dir.mkdir(parents=True)
            # A single regex routes every HTTP host to this server. Unknown
            # paths return 404; it cannot forward to production services.
            settings = '[settings]\nminimum_release_age = "0"\nlockfile = true\n[settings.url_replacements]\n'
            settings += "'regex:^https?://([^/]+)/(.*)$' = " + json.dumps(f"http://127.0.0.1:{server.server_port}/$1/$2") + "\n"
            (global_dir / "config.toml").write_text(settings)
            recipe = global_dir / "conf.d/kagikae-install.toml"
            recipe.parent.mkdir()

            def request(version, target=recipe, postinstall=True):
                fields = f'version = "{version}", pubkey = {json.dumps(public_key)}, allow_unlogged = true'
                if postinstall:
                    fields += ', postinstall = \'"$MISE_TOOL_INSTALL_PATH/kae" init\''
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text("[tools]\n" + json.dumps(TOOL) + " = { " + fields + " }\n")

            native_os = "darwin" if platform.system() == "Darwin" else "linux"
            native_arch = "arm64" if platform.machine() in ("arm64", "aarch64") else "amd64"
            binary = root / "kae"

            def publish(version, defect="", build=False):
                tag = "v" + version
                stage = root / version
                stage.mkdir()
                if build:
                    (source / "internal/cmd/cmd.go").write_text(original.replace('toolVersion = "v0.21.0"', f'toolVersion = "{tag}"', 1))
                    run([go, "build", "-o", binary, "."], source)
                shutil.copy2(binary, stage / "kae")
                completions = stage / "completions"
                completions.mkdir()
                for shell in ("bash", "zsh", "fish"):
                    (completions / f"kae.{shell}").write_text(run([binary, "completion", shell], stage))
                system = "windows" if defect == "platform" else native_os
                name = f"kae_{version}_{system}_{native_arch}.tar.gz"
                archive = stage / name
                with tarfile.open(archive, "w:gz") as tar:
                    for name_in_archive in ("kae", "completions/kae.bash", "completions/kae.zsh", "completions/kae.fish"):
                        tar.add(stage / name_in_archive, arcname=name_in_archive)
                project = PROJECT + "-other" if defect == "project" else PROJECT
                base = f"https://github.com/webkaz-labs/kagikae/releases/download/{tag}"
                args = [packslip, "create", "--project", project, "--version", version, "--tag", tag,
                        "--source-repo", "https://github.com/webkaz-labs/kagikae", "--commit", COMMIT,
                        "--url-base", base, "--bin", "kae", "--key", other_key if defect == "key" else key,
                        "--no-log", "--out", stage]
                for shell in ("bash", "zsh", "fish"):
                    args += ["--resource", f"completion/{shell}=archive:completions/kae.{shell}"]
                run(args + [archive], root)
                if defect == "digest":
                    with archive.open("ab") as out:
                        out.write(b"tampered")
                assets = []
                for asset in (archive, stage / "packslip.sigstore.json"):
                    url = base + "/" + asset.name
                    assets.append({"id": len(files) + 1, "name": asset.name, "size": asset.stat().st_size,
                                   "browser_download_url": url, "url": url, "content_type": "application/octet-stream"})
                    if not (defect == "missing" and asset == archive):
                        files["/github.com/" + url.split("github.com/", 1)[1]] = asset
                releases[tag] = {"id": len(releases) + 1, "tag_name": tag, "name": tag, "draft": False,
                                 "prerelease": False, "created_at": "2026-09-01T00:00:00Z",
                                 "published_at": "2026-09-01T00:00:00Z", "assets": assets}

            publish("0.21.0", build=True)
            publish("0.21.1", build=True)
            defects = ("key", "project", "digest", "platform", "missing")
            for index, defect in enumerate(defects, 2):
                publish(f"0.21.{index}", defect)
            request("0.21.0")
            run([mise, "install"], home)
            config = Path(env["XDG_CONFIG_HOME"]) / "kagikae/config.toml"
            assert config.exists(), "tool-level postinstall did not initialize the selected config root"
            active = Path(run([mise, "where", TOOL + "@0.21.0"], home).strip())
            assert run([active / "kae", "version"], home).strip() == "kae v0.21.0"
            assert not (Path(env["XDG_STATE_HOME"]) / "kagikae/installations").exists(), "managed install acquired a direct receipt"
            saved = config.read_bytes()
            config.unlink()
            run([mise, "install"], home)
            assert not config.exists(), "tool-level postinstall ran on a no-op install"
            run([active / "kae", "init"], home)
            saved += b'\n[profiles.main.accounts]\nclaude = "main"\n[profiles.side.accounts]\nclaude = "side"\n'
            config.write_bytes(saved)
            credential = Path(env["XDG_DATA_HOME"]) / "kagikae/stores/claude/side/fixture-credential"
            credential.parent.mkdir(parents=True)
            credential.write_bytes(b"fixture credential bytes")
            run([active / "kae", "completion", "bash", "--install"], home, input_text="2\n")
            fragment = global_dir / "conf.d/kagikae.toml"
            fragment_bytes = fragment.read_bytes()
            custom_completion = Path(env["XDG_DATA_HOME"]) / "bash-completion/completions/kae"
            custom_completion.parent.mkdir(parents=True)
            custom_completion.write_text("# fixture custom completion\ncomplete -W custom kae\n")
            results.append("initial install, init recipe, no-op and direct-receipt exclusion")

            project_dir = root / "side-project"
            project_config = project_dir / "mise.toml"
            request("0.21.0", project_config, False)
            run([mise, "trust", project_config], home)
            shared_project = root / "main-app"
            request("0.21.0", shared_project / "mise.toml", False)
            run([mise, "trust", shared_project / "mise.toml"], home)
            before_update = recipe.read_bytes()
            run([mise, "use", "--dry-run", "--path", recipe, TOOL + "@0.21.1"], home)
            assert recipe.read_bytes() == before_update, "update preview changed configuration"
            run([mise, "use", "--path", recipe, TOOL + "@0.21.1"], home)
            assert "postinstall" in recipe.read_text(), "update discarded the existing setup recipe"
            assert run([mise, "exec", "--", "kae", "version"], home).strip() == "kae v0.21.1"
            assert run([mise, "exec", "--", "kae", "version"], project_dir).strip() == "kae v0.21.0"
            assert config.read_bytes() == saved, "upgrade init changed existing configuration"
            assert fragment.read_bytes() == fragment_bytes, "upgrade changed generated global completion"
            assert credential.read_bytes() == b"fixture credential bytes"
            for cwd, version in ((home, "0.21.1"), (project_dir, "0.21.0")):
                installed = Path(run([mise, "where", TOOL], cwd).strip())
                for shell in ("bash", "zsh", "fish"):
                    got = run([mise, "completion", shell, "--tool", "kae"], cwd)
                    assert got == (installed / f"completions/kae.{shell}").read_text(), (version, shell)
                commands = run([mise, "exec", "--", "kae", "__complete", "commands"], cwd).splitlines()
                assert "uninstall" in commands
                flags = run([mise, "exec", "--", "kae", "__complete", "flags", "uninstall"], cwd)
                assert "--dir" in flags and "--dry-run" in flags
                profiles = run([mise, "exec", "--", "kae", "__complete", "profiles"], cwd).splitlines()
                assert "main" in profiles and "side" in profiles
            before_offline = len(requests)
            assert run([mise, "exec", "--", "kae", "version"], shared_project, extra={"MISE_OFFLINE": "1"}).strip() == "kae v0.21.0"
            assert len(requests) == before_offline, "offline reuse made an HTTP request"
            results.append("upgrade, project selection and static/dynamic completion")

            report = json.loads(run([mise, "exec", "--", "kae", "uninstall", "--dry-run", "--json"], home))
            assert report["binary"] == "pending"
            selected = Path(run([mise, "where", TOOL], home).strip())
            partial = json.loads(run([selected / "kae", "uninstall", "--yes", "--json"], home, expected=1))
            assert not partial["ok"] and partial["integrations"] == "incomplete"
            assert not fragment.exists(), "owned global completion survived teardown"
            assert "custom" in custom_completion.read_text(), "custom completion was removed"
            assert recipe.exists(), "user-owned install recipe was removed"
            assert config.read_bytes() == saved and credential.read_bytes() == b"fixture credential bytes"
            run([mise, "unuse", "--path", recipe, "--no-prune", TOOL], home)
            run([mise, "uninstall", TOOL + "@0.21.1"], home)
            assert run([mise, "exec", "--", "kae", "version"], project_dir).strip() == "kae v0.21.0"
            request("0.21.1")
            run([mise, "install"], home)
            assert config.read_bytes() == saved
            assert credential.read_bytes() == b"fixture credential bytes" and not fragment.exists()
            results.append("managed removal, retained project version and reinstall")

            for index, defect in enumerate(defects, 2):
                version = f"0.21.{index}"
                request(version)
                failure = run([mise, "install"], home, expected=1)
                reasons = {"key": "signature does not verify with the pinned key", "project": "the packslip is for",
                           "digest": "document says", "platform": "no artifact for", "missing": "404 Not Found"}
                assert reasons[defect] in failure, (defect, failure)
                results.append("refused " + defect)
            assert requests, "backend did not contact fixture service"
            print(json.dumps({"status": "success", "mise": "2026.9.3", "trust": "ephemeral key/unlogged fixture only", "checks": results, "requests": len(requests)}))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
