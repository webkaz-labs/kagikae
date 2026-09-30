// The only module that imports @microsoft/tui-test. Everything a scenario needs
// from a pseudo-terminal goes through the functions exported here, so a change
// of terminal library (or a new pin) touches this file and nothing else.
//
// What it guarantees, and why each one exists:
//
//   * The shell runs under `env -i`, not with an env option. tui-test merges the
//     parent's environment into whatever it is given (a parent-only variable was
//     measured leaking into the child), so the only isolation that holds is a
//     process whose whole environment is spelled out on the command line: a temp
//     HOME, every XDG root under it, no tool-home variable, and a PATH of the
//     freshly built kae's directory plus /usr/bin:/bin. `zsh -f` skips rc files,
//     so nothing on the machine defines the prompt or the `kae` function.
//   * kae is built once per run from this checkout into a temp directory. An
//     installed kae is never on PATH, and the real HOME and XDG roots are never
//     handed to the child.
//   * The palette is pinned, so the emulator's default colors cannot change what
//     a text or style assertion sees.
//   * Nothing waits by sleeping: a scenario waits for text to appear (bounded by
//     TIMEOUT_MS) or for the process to exit.
//   * runScenarios runs every scenario, catches each one on its own, then
//     reports; the exit status is non-zero if any failed. A run that stopped at
//     the first failure would hide the others.
//
// Not covered: a real login shell, another shell than zsh, colors (only text is
// asserted), and any terminal other than this emulator. docs/VALIDATION.md §
// Picker PTY suite says the same for a reader.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { TuiTest, uniqueSession } from "@microsoft/tui-test";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, "..", "..");
const TIMEOUT_MS = 15000;
const PROMPT = "READY> ";

const PALETTE = {
  foreground: "#d0d0d0",
  background: "#101010",
  black: "#000000",
  red: "#cd3131",
  green: "#0dbc79",
  yellow: "#e5e510",
  blue: "#2472c8",
  magenta: "#bc3fbc",
  cyan: "#11a8cd",
  white: "#e5e5e5",
  brightBlack: "#666666",
  brightRed: "#f14c4c",
  brightGreen: "#23d18b",
  brightYellow: "#f5f543",
  brightBlue: "#3b8eea",
  brightMagenta: "#d670d6",
  brightCyan: "#29b8db",
  brightWhite: "#ffffff",
  cursor: "#ffffff",
};

let world = null;

// setup builds kae once. Every path it returns lies under one temp directory
// that teardown removes.
export function setup() {
  if (world) return world;
  const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "kae-tui-")));
  const binDir = path.join(base, "bin");
  fs.mkdirSync(binDir);
  execFileSync("go", ["build", "-o", path.join(binDir, "kae"), "."], {
    cwd: REPO_ROOT,
    stdio: ["ignore", "inherit", "inherit"],
  });
  world = { base, binDir };
  return world;
}

export function teardown() {
  if (!world) return;
  fs.rmSync(world.base, { recursive: true, force: true });
  world = null;
}

// makeFixture creates a fresh HOME holding a git repository with `.claude/` at
// its root and a subdirectory. It sits under $HOME/work so the picker shows it
// as ~/work/repo, which keeps a snapshot independent of the temp directory name.
function makeFixture(name) {
  const { base } = setup();
  const home = path.join(base, name);
  const repo = path.join(home, "work", "repo");
  const sub = path.join(repo, "sub");
  fs.mkdirSync(path.join(repo, ".claude"), { recursive: true });
  fs.mkdirSync(sub, { recursive: true });
  for (const d of ["config", "data", "state", "cache"]) {
    fs.mkdirSync(path.join(home, ".xdg", d), { recursive: true });
  }
  execFileSync("/usr/bin/git", ["init", "-q"], { cwd: repo, stdio: "ignore" });
  return { home, repo, sub };
}

// openShell starts zsh in the fixture's subdirectory with the kae function
// defined the way an rc file defines it, and returns the scenario's handle.
async function openShell({ name, cols, rows }) {
  const { binDir } = setup();
  const fx = makeFixture(name);
  const xdg = (d) => path.join(fx.home, ".xdg", d);
  const t = new TuiTest(uniqueSession("kae"), { profile: { colors: PALETTE } });
  await t.run(
    "/usr/bin/env",
    [
      "-i",
      `HOME=${fx.home}`,
      `XDG_CONFIG_HOME=${xdg("config")}`,
      `XDG_DATA_HOME=${xdg("data")}`,
      `XDG_STATE_HOME=${xdg("state")}`,
      `XDG_CACHE_HOME=${xdg("cache")}`,
      `TMPDIR=${fx.home}`,
      `PATH=${binDir}:/usr/bin:/bin`,
      "KAE_CLAUDE_DRIVER=file",
      "GIT_CONFIG_GLOBAL=/dev/null",
      "GIT_CONFIG_NOSYSTEM=1",
      "TERM=xterm-256color",
      `PS1=${PROMPT}`,
      "/bin/zsh",
      "-f",
    ],
    { waitReady: false, cols, rows },
  );
  const s = makeHandle(t, fx);
  await s.expectText(PROMPT);
  await s.run(`eval "$(kae completion zsh)"`);
  await s.run(`cd '${fx.sub}'`);
  await s.run("clear");
  return s;
}

function makeHandle(t, fx) {
  const s = {
    fx,
    // run types a line and Enter, then waits for the output to go quiet; a
    // command that opens a picker returns once the picker has drawn.
    run: async (line) => {
      await t.submit(line);
      await t.waitIdle({ timeout: TIMEOUT_MS });
    },
    type: (text) => t.type(text),
    key: (name) => t.keyboard.press(name),
    resize: (cols, rows) => t.resize(cols, rows),
    size: () => t.getSize(),
    screen: () => t.text(),
    idle: () => t.waitIdle({ timeout: TIMEOUT_MS }),
    expectText: (text) => t.getByText(text, { regex: false }).expect({ timeout: TIMEOUT_MS }),
    expectNoText: (text) =>
      t.getByText(text, { regex: false }).expect({ not: true, timeout: TIMEOUT_MS }),
    // expectPwd asks the shell to compare, so a path wrapped by a narrow
    // terminal cannot make the screen text disagree with $PWD.
    expectPwd: async (dir, marker) => {
      await s.run(`[ "$PWD" = '${dir}' ] && echo "${marker}:same" || echo "${marker}:other"`);
      await s.expectText(`${marker}:same`);
    },
    snapshot: (name) => t.expectSnapshot(name, { update: process.env.KAE_TUI_UPDATE === "1" }),
    close: () => t.closeQuiet(),
  };
  return s;
}

// withShell runs body against a fresh shell and always closes it.
export async function withShell(opts, body) {
  const s = await openShell(opts);
  try {
    await body(s);
  } finally {
    await s.close();
  }
}

// runScenarios runs the scenarios whose tag matches (all when tag is empty),
// each caught on its own, then prints one line per scenario and returns the
// number that failed.
export async function runScenarios(scenarios, tag) {
  process.chdir(HERE); // expectSnapshot writes __snapshots__/ under the cwd
  const chosen = scenarios.filter((sc) => !tag || sc.tags?.includes(tag));
  if (chosen.length === 0) {
    console.error(`no scenario is tagged ${JSON.stringify(tag)}`);
    return 1;
  }
  const results = [];
  try {
    for (const sc of chosen) {
      const t0 = performance.now();
      try {
        await sc.run();
        results.push({ sc, ok: true, ms: performance.now() - t0 });
      } catch (err) {
        results.push({ sc, ok: false, ms: performance.now() - t0, err });
      }
    }
  } finally {
    teardown();
  }
  for (const r of results) {
    const ms = Math.round(r.ms);
    console.log(`${r.ok ? "ok  " : "FAIL"} ${r.sc.name} (${ms} ms)`);
    if (!r.ok) console.log(`     ${String(r.err?.stack ?? r.err).split("\n").join("\n     ")}`);
  }
  const failed = results.filter((r) => !r.ok).length;
  console.log(`${results.length - failed}/${results.length} scenarios passed`);
  return failed;
}
