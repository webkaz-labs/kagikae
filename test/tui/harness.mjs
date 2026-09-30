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
//   * Expected text can never be satisfied by the echoed command line. The handle
//     records every line and key text it types, and expectText throws when the
//     expectation is a substring of one of them. A command therefore prints its
//     answer as `echo "<marker>:$?"` and the scenario expects `<marker>:0`.
//   * A snapshot that does not exist fails; KAE_TUI_UPDATE=1 is the only way one
//     is written or changed.
//   * runScenarios runs every scenario (up to KAE_TUI_JOBS at once, see
//     DEFAULT_JOBS), catches each one on its own, then reports in declaration
//     order; the exit status is non-zero if any failed. A run that stopped at
//     the first failure would hide the others.
//
// The environment sets no NO_COLOR on purpose: only text is asserted, and colour
// is the Go tests' business.
//
// Not covered: a real login shell, another shell than zsh, colors (only text is
// asserted), and any terminal other than this emulator. docs/VALIDATION.md §
// Picker PTY suite says the same for a reader.

import { AsyncLocalStorage } from "node:async_hooks";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { TuiTest, uniqueSession } from "@microsoft/tui-test";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, "..", "..");
// A positive finite number from an environment value, or undefined.
function positiveFinite(value) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

// Per-step wait, 15 s unless KAE_TUI_TIMEOUT_MS overrides it (docs/VALIDATION.md
// § Picker PTY suite).
const TIMEOUT_MS = positiveFinite(process.env.KAE_TUI_TIMEOUT_MS) ?? 15000;
const BUILD_TIMEOUT_MS = 300000;
const SCENARIO_TIMEOUT_MS = 90000;
const PROMPT = "READY> ";
// Scenarios in flight at once: 4, or the CPUs available to this process when
// fewer. KAE_TUI_JOBS overrides; 1 runs them one by one.
const DEFAULT_JOBS = Math.min(4, os.availableParallelism());

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
// Every shell still open, across scenarios. A scenario running under `owner`
// also records its shells in its own set, so one finishing closes only its own.
const sessions = new Set();
const owner = new AsyncLocalStorage();

// setup builds kae once. Every path it returns lies under one temp directory
// that teardown removes, also when the build fails.
export function setup() {
  if (world) return world;
  const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "kae-tui-")));
  try {
    const binDir = path.join(base, "bin");
    fs.mkdirSync(binDir);
    execFileSync("go", ["build", "-o", path.join(binDir, "kae"), "."], {
      cwd: REPO_ROOT,
      stdio: ["ignore", "inherit", "inherit"],
      timeout: BUILD_TIMEOUT_MS,
      // The cache the sibling mise tasks use, so this build is warm after them and
      // writes nothing under the real HOME.
      env: {
        ...process.env,
        GOCACHE: process.env.GOCACHE || path.join(process.env.TMPDIR || "/tmp", "kae-gocache"),
      },
    });
    world = { base, binDir };
  } catch (err) {
    fs.rmSync(base, { recursive: true, force: true });
    throw err;
  }
  return world;
}

function closeAll(set) {
  return Promise.all([...set].map((t) => t.closeQuiet().catch(() => {})));
}

function removeWorld() {
  if (!world) return;
  fs.rmSync(world.base, { recursive: true, force: true });
  world = null;
}

// teardown closes every shell still open (a scenario that failed or timed out
// leaves its shell here) and removes the temp directory.
export async function teardown() {
  await closeAll(sessions);
  sessions.clear();
  removeWorld();
}

// 128 plus the signal number, the shell convention: SIGINT 130, SIGTERM 143.
for (const [sig, code] of [["SIGINT", 130], ["SIGTERM", 143]]) {
  process.once(sig, () => {
    removeWorld();
    process.exit(code);
  });
}

// makeFixture creates a fresh HOME holding a git repository with `.claude/` at
// its root and a subdirectory. It sits under $HOME/work so the picker shows it
// as ~/work/repo, which keeps a snapshot independent of the temp directory name.
function makeFixture(name, repoName) {
  const { base } = setup();
  const home = path.join(base, name);
  const repo = path.join(home, "work", repoName);
  const sub = path.join(repo, "sub");
  fs.mkdirSync(path.join(repo, ".claude"), { recursive: true });
  fs.mkdirSync(sub, { recursive: true });
  for (const d of ["config", "data", "state", "cache", "run"]) {
    fs.mkdirSync(path.join(home, ".xdg", d), { recursive: true });
  }
  fs.mkdirSync(path.join(home, "tmp"));
  // The operator's git configuration must not reach the fixture either.
  execFileSync("/usr/bin/git", ["init", "-q"], {
    cwd: repo,
    stdio: "ignore",
    env: { PATH: "/usr/bin:/bin", HOME: home, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_NOSYSTEM: "1" },
  });
  return { home, repo, sub };
}

// openShell starts zsh in the fixture's subdirectory with the kae function
// defined the way an rc file defines it, and returns the scenario's handle.
async function openShell({ name, cols, rows, repoName = "repo" }) {
  const { binDir } = setup();
  const fx = makeFixture(name, repoName);
  const xdg = (d) => path.join(fx.home, ".xdg", d);
  const t = new TuiTest(uniqueSession("kae"), { profile: { colors: PALETTE } });
  sessions.add(t);
  owner.getStore()?.add(t);
  await t.run(
    "/usr/bin/env",
    [
      "-i",
      `HOME=${fx.home}`,
      `XDG_CONFIG_HOME=${xdg("config")}`,
      `XDG_DATA_HOME=${xdg("data")}`,
      `XDG_STATE_HOME=${xdg("state")}`,
      `XDG_CACHE_HOME=${xdg("cache")}`,
      `XDG_RUNTIME_DIR=${xdg("run")}`,
      `TMPDIR=${path.join(fx.home, "tmp")}`,
      "LC_ALL=C",
      "TZ=UTC",
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
  s.forgetTyped(); // setup lines are not part of any scenario's expectations
  return s;
}

function makeHandle(t, fx) {
  const typed = [];
  const refuseEchoed = (fn, shown, matches) => {
    const echoed = typed.find(matches);
    if (echoed !== undefined) {
      throw new Error(
        `${fn}(${shown}) is satisfied by the typed line ${JSON.stringify(echoed)}; print it from the command, for example echo "marker:$?"`,
      );
    }
  };
  const s = {
    fx,
    forgetTyped: () => {
      typed.length = 0;
    },
    // run types a line and Enter, then waits for the output to go quiet; a
    // command that opens a picker returns once the picker has drawn.
    run: async (line) => {
      typed.push(line);
      await t.submit(line);
      await t.waitIdle({ timeout: TIMEOUT_MS });
    },
    type: (text) => {
      typed.push(text);
      return t.type(text);
    },
    key: (name) => t.keyboard.press(name),
    resize: (cols, rows) => t.resize(cols, rows),
    size: () => t.getSize(),
    screen: () => t.text(),
    idle: () => t.waitIdle({ timeout: TIMEOUT_MS }),
    // expectText refuses an expectation that the terminal's echo of what was
    // typed would satisfy, because that assertion could never fail.
    expectText: (text) => {
      refuseEchoed("expectText", JSON.stringify(text), (line) => line.includes(text));
      return t.getByText(text, { regex: false }).expect({ timeout: TIMEOUT_MS });
    },
    // expectMatch is expectText for a pattern, with the same refusal. Only the
    // pattern's source reaches the terminal search; its flags do not.
    expectMatch: (re) => {
      refuseEchoed("expectMatch", String(re), (line) => re.test(line));
      return t.getByText(re.source, { regex: true }).expect({ timeout: TIMEOUT_MS });
    },
    expectNoText: (text) =>
      t.getByText(text, { regex: false }).expect({ not: true, timeout: TIMEOUT_MS }),
    // expectPwd asks the shell to compare, so a path wrapped by a narrow
    // terminal cannot make the screen text disagree with $PWD. The status is
    // printed from `$?`, so the typed line cannot contain the expected `:0`.
    expectPwd: async (dir, marker) => {
      await s.run(`[ "$PWD" = '${dir}' ]; echo "${marker}:$?"`);
      await s.expectText(`${marker}:0`);
    },
    // snapshot fails when the file is missing, so a renamed or unchecked-out
    // baseline cannot turn into a silent new one.
    snapshot: (name) => {
      const update = process.env.KAE_TUI_UPDATE === "1";
      if (!update && !fs.existsSync(path.join(HERE, "__snapshots__", `${name}.snap`))) {
        throw new Error(`snapshot ${name} is missing; run with KAE_TUI_UPDATE=1 and review it`);
      }
      return t.expectSnapshot(name, { update });
    },
  };
  return s;
}

// withShell runs body against a fresh shell. The shell is closed by teardown
// even when opening it fails part way.
export async function withShell(opts, body) {
  const s = await openShell(opts);
  await body(s);
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
  try {
    setup(); // one build for the run; a failure is one failure, not one per scenario
  } catch (err) {
    console.error(`building kae failed: ${err?.message ?? err}`);
    return 1;
  }
  // Scenarios run concurrently, at most `jobs` at a time. Each has its own
  // shell, HOME and temp directory, so they share only the built binary. The
  // report below keeps the declaration order whatever the finishing order.
  const results = new Array(chosen.length);
  const jobs = Math.max(1, Math.min(chosen.length, Number(process.env.KAE_TUI_JOBS) || DEFAULT_JOBS));
  let next = 0;
  const runOne = async (sc, i) => {
    const t0 = performance.now();
    const mine = new Set();
    let timer;
    try {
      const deadline = new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new Error(`scenario exceeded ${SCENARIO_TIMEOUT_MS} ms`)),
          SCENARIO_TIMEOUT_MS,
        );
      });
      await Promise.race([owner.run(mine, () => sc.run()), deadline]);
      results[i] = { sc, ok: true, ms: performance.now() - t0 };
    } catch (err) {
      results[i] = { sc, ok: false, ms: performance.now() - t0, err };
    } finally {
      clearTimeout(timer);
      await closeAll(mine);
      for (const t of mine) sessions.delete(t);
    }
  };
  const worker = async () => {
    while (next < chosen.length) {
      const i = next++;
      await runOne(chosen[i], i);
    }
  };
  try {
    await Promise.all(Array.from({ length: jobs }, worker));
  } finally {
    await teardown();
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
