package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

func TestProductionWriteObservation(t *testing.T) {
	values := map[string]string{"USER": "main"}
	env := adapter.Env{GOOS: "darwin", Home: t.TempDir(), Getenv: func(k string) string { return values[k] }, LookupEnv: func(k string) (string, bool) { v, ok := values[k]; return v, ok }}
	got, err := observe(env)
	want := []string{"add-generic-password", "-U", "-s", "Claude Code-credentials", "-a", "main", "-w", "<redacted>"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("observation = %v, %v; want %v", got, err, want)
	}
	values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = ""
	if _, err := observe(env); err == nil {
		t.Fatal("empty secure-storage value must retain adapter refusal")
	}
}

func TestObserverRefusesUnmodeledSubprocesses(t *testing.T) {
	o := &observer{}
	for _, command := range []struct{ name, operation string }{{"/usr/bin/security", "find-generic-password"}, {"security", "delete-generic-password"}, {"other", "read"}} {
		if _, _, code := o.Run(context.Background(), command.name, command.operation); code == 0 {
			t.Fatal("unexpected subprocess accepted")
		}
	}
	if _, _, code := o.RunInput(context.Background(), "", "security", "add-generic-password"); code == 0 {
		t.Fatal("stdin subprocess accepted")
	}
}
