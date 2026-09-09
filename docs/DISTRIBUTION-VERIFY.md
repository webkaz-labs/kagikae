# Distribution verification reuse

The shared distribution verifier owns archive/checksum validation, source and
Packslip signature verification, statement comparison, and the order before native
execution. A trusted product specification supplies exact expected artifacts,
archive members, source identity and completion resources. The specification is
input from the operator's trusted checkout, never from the release being checked.

Product adapters own historical release differences, initialization, receipts and
retained data. Shell adapters own the commands that request candidates; shared
checks observe candidates and registration without claiming interactive TTY coverage.
Fixture HTTP routing is an allowlist, not a network sandbox. Unknown routes and
authorization headers fail the fixture's final verdict.

The portable command and its specification are maintained with the implementation;
see [DOCUMENTATION.md](DOCUMENTATION.md) for reuse entrypoints.
