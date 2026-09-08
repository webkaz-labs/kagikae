"""Exercise mise registration and generated completions in real clean shells.

Zsh's compadd is captured as in mise 2026.9.3's own auto-completion fixture;
the loader and generated function execute, without pretending to drive a TTY.
"""

import shutil


def verify_shells(run, root, project, home, mise):
    results = []
    for shell in ("bash", "zsh"):
        executable = shutil.which(shell)
        if not executable:
            raise RuntimeError(f"{shell} is required for completion acceptance")
        for route in ("auto", "manual"):
            log = root / f"{shell}-{route}.calls"
            setup = r'''
set -e
kae() { command kae version >> "$CALL_LOG"; command kae "$@"; }
cd "$PROJECT_DIR"
'''
            if shell == "bash":
                setup += r'''
complete -W custom kae
exercise() {
  COMP_WORDS=(kae ''); COMP_CWORD=1; COMP_LINE='kae '; COMP_POINT=4
  _kae
  printf '%s\n' "${COMPREPLY[@]}"
  COMP_WORDS=(kae use si); COMP_CWORD=2; COMP_LINE='kae use si'; COMP_POINT=10
  _kae
  printf '%s\n' "${COMPREPLY[@]}"
}
'''
                loader = '__mise_complete_kae || test "$?" = 124\n'
                restored = 'complete -p kae\n'
                expected_custom = "-W 'custom' kae"
                args = [executable, "--noprofile", "--norc"]
            else:
                setup += r'''
autoload -Uz compinit
compinit -i
_custom_kae() { :; }
compdef _custom_kae kae
typeset -A compstate
compstate[nmatches]=0
compadd() { print -rl -- "$@"; }
exercise() {
  words=(kae ''); CURRENT=2
  _kae
  words=(kae use si); CURRENT=3
  _kae
}
'''
                loader = ""
                restored = 'print -r -- "${_comps[kae]}"\n'
                expected_custom = "_custom_kae"
                args = [executable, "-f"]
            if route == "auto":
                setup += f'eval "$("$MISE_BIN" activate {shell})"\n'
                select = f'eval "$("$MISE_BIN" hook-env -s {shell} --force)"\n' + loader
            else:
                select = f'eval "$("$MISE_BIN" env -s {shell})"\neval "$("$MISE_BIN" completion {shell} --tool kae)"\n'
            script = setup + select + 'printf "FIRST\\n"\nexercise\n'
            script += 'printf "SWITCH\\n" >> "$CALL_LOG"\ncd "$GLOBAL_DIR"\n'
            if route == "manual":
                # Raw source is a snapshot, not mise's version-aware loader.
                script += f'eval "$("$MISE_BIN" env -s {shell})"\nprintf "STALE\\n"\nexercise\n'
            script += select + 'printf "SECOND\\n"\nexercise\n'
            if route == "auto":
                script += 'eval "$("$MISE_BIN" deactivate)"\nprintf "RESTORED\\n"\n' + restored
            output = run(args, home, extra={"CALL_LOG": str(log), "PROJECT_DIR": str(project),
                                           "GLOBAL_DIR": str(home), "MISE_BIN": mise}, input_text=script)
            first = output.split("FIRST\n", 1)[1].split("STALE\n" if route == "manual" else "SECOND\n", 1)[0]
            second = output.split("SECOND\n", 1)[1].split("RESTORED\n", 1)[0]
            for text, version, absent in ((first, "0.21.0", "0.21.1"), (second, "0.21.1", "0.21.0")):
                assert "fixture-static-" + version in text, (shell, route, output)
                assert "fixture-static-" + absent not in text, (shell, route, output)
                assert "side" in text.splitlines(), (shell, route, output)
            if route == "manual":
                stale = output.split("STALE\n", 1)[1].split("SECOND\n", 1)[0]
                assert "fixture-static-0.21.0" in stale and "fixture-static-0.21.1" not in stale
            else:
                assert expected_custom in output.split("RESTORED\n", 1)[1], output
            before, after = log.read_text().split("SWITCH\n")
            assert before.splitlines() and set(before.splitlines()) == {"kae v0.21.0"}, before
            assert after.splitlines() and set(after.splitlines()) == {"kae v0.21.1"}, after
            results.append(f"{shell} same-shell {route}: static version, dynamic binary/profile and " +
                           ("custom registration restored" if route == "auto" else "raw-source reload boundary"))
    results.append("fish runtime unverified: resource retrieval only; fish is not required by this fixture")
    return results
