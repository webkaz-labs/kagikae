// Picker scenarios against the built binary. docs/VALIDATION.md § Picker PTY suite
// says how to run them and what they do not cover; harness.mjs owns the terminal.
//
//   node run.mjs            every scenario
//   node run.mjs --tag fast the scenarios tagged fast (one of the full suite)
//
// The fixture is a git repository with `.claude/` at its root and a `sub`
// directory, and the shell starts in `sub`. `kae cd` with no target lists the
// claude project (root and `.claude/`) and the repository root, so the filter
// "repository" narrows the set to the repository root row alone.

import { runScenarios, withShell } from "./harness.mjs";

const FILTER_HINT = "type to filter";
// The key hint line is drawn for as long as the picker is open, whatever the filter.
const FOOTER = "enter choose";
// Long enough that its row does not fit in 80 columns, so a redraw at that width
// has to cut it.
const LONG_REPO = "repo-with-a-deliberately-long-directory-name-so-the-80-column-redraw-must-cut-it";
// The second row of the claude group, marked as the selection.
const SELECTED_CLAUDE_DIR = />\s+\.claude/;
const ROOT_ROW = "> ~/work/repo  repository-root";

// chooseRepositoryRoot is the journey scenarios 1 and 2 share: open the picker,
// filter, clear the filter with Esc (the picker stays open), filter again,
// choose the repository root, and require exit status 0 and the shell standing
// in the repository root.
async function chooseRepositoryRoot(s, marker) {
  await s.run(`kae cd; echo "${marker}-rc:$?"`);
  await s.expectText(FILTER_HINT);
  await s.type("repository");
  await s.expectText("> repository");
  await s.key("Escape");
  await s.expectText("claude");
  await s.type("repository");
  await s.expectText("> repository");
  await s.key("Enter");
  await s.expectText(`${marker}-rc:0`);
  await s.expectNoText(FOOTER);
  await s.expectPwd(s.fx.repo, `${marker}-pwd`);
}

const scenarios = [
  {
    name: "1 kae cd chooses the repository root (80x24)",
    tags: ["fast"],
    run: () =>
      withShell({ name: "s1", cols: 80, rows: 24 }, (s) => chooseRepositoryRoot(s, "s1")),
  },
  {
    name: "2 same journey at 120x36 with a picker snapshot",
    run: () =>
      withShell({ name: "s2", cols: 120, rows: 36 }, async (s) => {
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.snapshot("picker-120x36");
        await s.key("Escape");
        await s.expectNoText(FOOTER);
        await s.run("clear");
        await chooseRepositoryRoot(s, "s2");
      }),
  },
  {
    name: "3 Esc on an empty filter cancels: 130, same directory, empty stdout and stderr",
    run: () =>
      withShell({ name: "s3", cols: 80, rows: 24 }, async (s) => {
        await s.run('kae cd; echo "s3-rc:$?"');
        await s.expectText(FILTER_HINT);
        await s.key("Escape");
        await s.expectText("s3-rc:130");
        await s.expectNoText(FOOTER);
        await s.expectPwd(s.fx.sub, "s3-pwd");
        await s.run("clear");
        // The entry point the function wraps: stdout captured, stderr to a file.
        await s.run(
          'out=$(command kae __cd 2>"$HOME/s3.err"); rc=$?; [ -z "$out" ] && [ ! -s "$HOME/s3.err" ]; echo "s3-out:$rc:$?"',
        );
        await s.expectText(FILTER_HINT);
        await s.key("Escape");
        await s.expectText("s3-out:130:0");
      }),
  },
  {
    name: "4 Esc with a filter clears it, the second Esc cancels",
    run: () =>
      withShell({ name: "s4", cols: 80, rows: 24 }, async (s) => {
        await s.run('kae cd; echo "s4-rc:$?"');
        await s.expectText(FILTER_HINT);
        await s.type("repository");
        await s.expectText("> repository");
        await s.key("Escape");
        await s.expectText(FILTER_HINT);
        await s.expectText("claude");
        await s.key("Escape");
        await s.expectNoText(FOOTER);
        await s.expectText("s4-rc:130");
      }),
  },
  {
    name: "5 Ctrl-C cancels with 130 and restores the terminal modes",
    run: () =>
      withShell({ name: "s5", cols: 80, rows: 24 }, async (s) => {
        // A non-zsh parent: interactive zsh resets the tty modes before each
        // prompt and would hide a kae that leaves raw mode on. sh prints the
        // modes itself, straight after kae returns. The check is positive: stty
        // must succeed and list both icanon and echo, so a missing or failing
        // stty leaves s5-cooked at a non-zero status instead of passing.
        await s.run(
          `sh -c 'out=$(kae __cd); echo "s5-rc:$?"; m=$(stty -a) && printf "%s\\n" "$m" | grep -Eq "(^| )icanon( |;|\$)" && printf "%s\\n" "$m" | grep -Eq "(^| )echo( |;|\$)"; echo "s5-cooked:$?"'`,
        );
        await s.expectText(FILTER_HINT);
        await s.key("Ctrl+C");
        await s.expectText("s5-rc:130");
        await s.expectText("s5-cooked:0");
      }),
  },
  {
    name: "6 --pick opens the picker although a current place exists",
    run: () =>
      withShell({ name: "s6", cols: 80, rows: 24 }, async (s) => {
        // Without --pick this request has one place and moves there at once.
        await s.run("kae cd repo --pick");
        await s.expectText(FILTER_HINT);
        await s.expectText(ROOT_ROW);
        await s.key("Enter");
        await s.expectNoText(FOOTER);
        await s.expectPwd(s.fx.repo, "s6-pwd");
      }),
  },
  {
    name: "7 no terminal on stdin lists candidates on stderr, nothing on stdout, exits 64",
    run: () =>
      withShell({ name: "s7", cols: 80, rows: 24 }, async (s) => {
        await s.run('kae cd </dev/null >"$HOME/s7.out" 2>"$HOME/s7.err"; echo "s7-rc:$?"');
        await s.expectText("s7-rc:64");
        await s.expectNoText(FOOTER);
        // A candidate line is `  kae cd <group> --at <n>  <path>`. The whole line
        // is matched, so a wrong group word or a longer path at --at 1 (such as
        // `<repo>/.claude`) fails.
        await s.run(
          `grep -qxF -- '  kae cd repo --at 1  ${s.fx.repo}' "$HOME/s7.err" && [ ! -s "$HOME/s7.out" ]; echo "s7-list:$?"`,
        );
        await s.expectText("s7-list:0");
        await s.expectPwd(s.fx.sub, "s7-pwd");
      }),
  },
  {
    name: "8 resizing 120x36 to 80x24 redraws with the moved selection and the filter",
    run: () =>
      withShell({ name: "s8", cols: 120, rows: 36, repoName: LONG_REPO }, async (s) => {
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.type("repo");
        await s.expectText("> repo");
        await s.key("Down"); // off the first row, which is the default
        await s.expectMatch(SELECTED_CLAUDE_DIR);
        // At 120 columns the long path fits, so nothing is cut yet.
        await s.expectText(`~/work/${LONG_REPO}`);
        await s.expectNoText("…");
        await s.resize(80, 24);
        await s.idle();
        const size = await s.size();
        if (size.cols !== 80 || size.rows !== 24) {
          throw new Error(`resize did not take: ${JSON.stringify(size)}`);
        }
        // Only a redraw at the new width can cut a path from the left, so the
        // ellipsis shows the picker reacted; the uncut path must be gone.
        await s.expectText("…");
        await s.expectNoText(`~/work/${LONG_REPO}`);
        await s.expectText("> repo");
        await s.expectMatch(SELECTED_CLAUDE_DIR);
        await s.expectText(FOOTER);
        await s.key("Enter");
        await s.expectNoText(FOOTER);
        await s.expectPwd(`${s.fx.repo}/.claude`, "s8-pwd");
      }),
  },
  {
    name: "9 an existing claude session directory is a row that the filter projects selects",
    run: () =>
      withShell({ name: "s9", cols: 120, rows: 36 }, async (s) => {
        // The row's path comes from kae; the Go tests own how its name is derived.
        // The directory is created here because a missing one is not offered.
        await s.run('d=$(kae ls claude --at 2) && mkdir -p "$d"; echo "s9-mk:$?"');
        await s.expectText("s9-mk:0");
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.expectText("session");
        await s.type("projects");
        await s.expectText("> projects");
        await s.key("Enter");
        await s.expectNoText(FOOTER);
        await s.run('[ "$PWD" = "$d" ]; echo "s9-pwd:$?"');
        await s.expectText("s9-pwd:0");
      }),
  },
];

const tagAt = process.argv.indexOf("--tag");
const tag = tagAt === -1 ? "" : process.argv[tagAt + 1];
if (tagAt !== -1 && !tag) {
  console.error("usage: node run.mjs [--tag <tag>]");
  process.exit(2);
}
const failed = await runScenarios(scenarios, tag);
process.exit(failed === 0 ? 0 : 1);
