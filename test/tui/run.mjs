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
const ROOT_ROW = "> ~/work/repo  repository-root";

// chooseRepositoryRoot is the journey scenarios 1 and 2 share: open the picker,
// narrow to the repository root, choose it, and require exit status 0 and the
// shell standing in the repository root.
async function chooseRepositoryRoot(s, marker) {
  await s.run(`kae cd; echo "${marker}-rc:$?"`);
  await s.expectText(FILTER_HINT);
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
    name: "3 Esc on an empty filter cancels: 130, same directory, empty stdout",
    run: () =>
      withShell({ name: "s3", cols: 80, rows: 24 }, async (s) => {
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.key("Escape");
        await s.expectNoText(FOOTER);
        await s.expectText("READY> kae cd");
        await s.run('echo "s3-rc:$?"');
        await s.expectText("s3-rc:130");
        await s.expectPwd(s.fx.sub, "s3-pwd");
        await s.run("clear");
        await s.run('out=$(command kae __cd); echo "s3-out:$?:[$out]"');
        await s.expectText(FILTER_HINT);
        await s.key("Escape");
        await s.expectText("s3-out:130:[]");
      }),
  },
  {
    name: "4 Esc with a filter clears it, the second Esc cancels",
    run: () =>
      withShell({ name: "s4", cols: 80, rows: 24 }, async (s) => {
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.type("repository");
        await s.expectText("> repository");
        await s.key("Escape");
        await s.expectText(FILTER_HINT);
        await s.expectText("claude");
        await s.key("Escape");
        await s.expectNoText(FOOTER);
        await s.run('echo "s4-rc:$?"');
        await s.expectText("s4-rc:130");
      }),
  },
  {
    name: "5 Ctrl-C cancels with 130 and leaves the terminal usable",
    run: () =>
      withShell({ name: "s5", cols: 80, rows: 24 }, async (s) => {
        await s.run('kae cd; echo "s5-rc:$?"');
        await s.expectText(FILTER_HINT);
        await s.key("Ctrl+C");
        await s.expectText("s5-rc:130");
        await s.run("echo s5-usable-$((20 + 22))");
        await s.expectText("s5-usable-42");
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
    name: "7 no terminal on stdin lists on stderr and exits 64",
    run: () =>
      withShell({ name: "s7", cols: 80, rows: 24 }, async (s) => {
        await s.run('kae cd </dev/null 2>"$HOME/s7.err"; echo "s7-rc:$?"');
        await s.expectText("s7-rc:64");
        await s.expectNoText(FOOTER);
        await s.run('grep -q "kae cd" "$HOME/s7.err" && echo "s7-list:yes" || echo "s7-list:no"');
        await s.expectText("s7-list:yes");
        await s.expectPwd(s.fx.sub, "s7-pwd");
      }),
  },
  {
    name: "8 resizing 120x36 to 80x24 keeps the filter and the selected row",
    run: () =>
      withShell({ name: "s8", cols: 120, rows: 36 }, async (s) => {
        await s.run("kae cd");
        await s.expectText(FILTER_HINT);
        await s.type("repo");
        await s.expectText("> repo");
        await s.resize(80, 24);
        await s.idle();
        const size = await s.size();
        if (size.cols !== 80 || size.rows !== 24) {
          throw new Error(`resize did not take: ${JSON.stringify(size)}`);
        }
        await s.expectText("> repo");
        await s.expectText("> ~/work/repo");
        await s.key("Enter");
        await s.expectNoText(FOOTER);
        await s.expectPwd(s.fx.repo, "s8-pwd");
      }),
  },
];

const tagAt = process.argv.indexOf("--tag");
const tag = tagAt === -1 ? "" : process.argv[tagAt + 1];
const failed = await runScenarios(scenarios, tag);
process.exit(failed === 0 ? 0 : 1);
