// Package runnertest provides the shared runner.Runner test double used by
// packages that stub subprocess output (secret, keychain). Tests needing
// stateful command simulation define their own fakes instead.
package runnertest

import "context"

// Fake is a canned-response runner.Runner. It records the last invocation.
//
// **When the argv is part of the contract, assert Args — not only the reply.** A
// test that fabricates a subprocess's output is, by construction, blind to what
// was asked: it will keep passing after a flag is dropped from the command,
// because the fake answers the same either way. This shipped: removing
// `--show-prefix` from `ensureGitExcluded`'s `git rev-parse` left every stubbed
// case green (internal/cmd/fragment_test.go). Two habits follow — check Args
// whenever a flag or subcommand carries meaning, and put any accompanying
// real-tool case where the value under test is **non-empty**, since a case that
// exercises the empty value cannot tell a missing flag from a present one.
type Fake struct {
	Stdout string
	Stderr string
	Code   int

	Name  string
	Args  []string
	Stdin string
}

func (f *Fake) Run(_ context.Context, name string, args ...string) (string, string, int) {
	f.Name = name
	f.Args = append([]string(nil), args...)
	return f.Stdout, f.Stderr, f.Code
}

func (f *Fake) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	f.Stdin = stdin
	return f.Run(ctx, name, args...)
}
