"""Safe harness controls: never launch an installed upstream or real security."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import verify


class VerificationTests(unittest.TestCase):
    def test_agreement_and_negative_controls(self):
        write = ["add-generic-password", "-s", "service", "-a", "main"]
        read = ["find-generic-password", "-s", "service", "-a", "main"]
        verify.compare(write, [read])
        for writes, reads in [(write, []), ([], [read]),
                              (write, [read[:-1] + ["side"]]),
                              (write, [read[:2] + ["wrong"] + read[3:]]),
                              (write, [["delete-generic-password"]]),
                              (write, [["find-generic-password"]])]:
            with self.subTest(reads=reads), self.assertRaises(verify.Refused):
                verify.compare(writes, reads)

    def test_unreviewed_bypass_fixtures_never_execute(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # Model both bypass classes without ever invoking either API.
            for body in (b"/usr/bin/security", b"SecItemCopyMatching", b"#!/bin/sh\nexit 0\n"):
                source = root / "unknown"
                source.write_bytes(body)
                with patch("verify.subprocess.run") as launch, self.assertRaises(verify.Refused):
                    verify.verified_copy(source, root / "copy")
                launch.assert_not_called()
            with self.assertRaises(verify.Refused):
                verify.verified_copy(None, root / "copy")

    def test_isolation_and_shim_preflight(self):
        repo = Path(__file__).resolve().parents[2]
        with verify.isolated_env(repo) as env:
            root = Path(env["HOME"])
            env["TMPDIR"] = str(root / "tmp")
            verify.install_shim(root, env)
            with self.assertRaises(verify.Refused):
                verify.preflight(env | {"XDG_DATA_HOME": "/outside"}, root / "shim/security")
            for key in ("HOME", "XDG_DATA_HOME", "TMPDIR"):
                missing = env.copy()
                del missing[key]
                with self.subTest(key=key), self.assertRaises(verify.Refused):
                    verify.preflight(missing, root / "shim/security")
            with self.assertRaises(verify.Refused):
                verify.preflight(env | {"PATH": "/usr/bin:/bin"}, root / "shim/security")
            (root / "shim/security").unlink()
            with self.assertRaises(verify.Refused):
                verify.preflight(env, root / "shim/security")

    def test_failed_preamble_never_yields_unowned_home(self):
        repo = Path(__file__).resolve().parents[2]
        for output in (b"", b"HOME=\0", b"HOME=/\0", b"HOME=relative\0"):
            with patch("verify.subprocess.run") as command:
                command.return_value.stdout = output
                with self.subTest(output=output), self.assertRaises(verify.Refused):
                    with verify.isolated_env(repo):
                        self.fail("invalid preamble yielded an environment")


if __name__ == "__main__":
    unittest.main()
