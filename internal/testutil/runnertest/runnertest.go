// Package runnertest provides the shared runner.Runner test double used by
// packages that stub subprocess output (secret, keychain). Tests needing
// stateful command simulation define their own fakes instead.
package runnertest

import "context"

// Fake is a canned-response runner.Runner. It records the last invocation.
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
