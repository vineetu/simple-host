#!/usr/bin/env python3
"""Exercise the real runner against TEST_PG_URL using tiny Go fixture packages.

Run: python3 scripts/test-integration_test.py
Only this test's unique runner databases and temporary files are used.
"""
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = r'''
package fixture

import (
    "fmt"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"
)

func TestPass(t *testing.T) {}

func TestFixture(t *testing.T) {
    mode := os.Getenv("FIXTURE_MODE")
    name := "PACKAGE"
    if mode == "skip" { t.Skip("DB_DSN unset: deliberately skipped fixture") }
    if mode == "interrupt" {
        u, err := url.Parse(os.Getenv("DB_DSN"))
        if err != nil { t.Fatal(err) }
        template := u.Path[1:]
        clone := fmt.Sprintf("%s_t%d_fixture", template, os.Getpid())
        u.Path = "/postgres"
        query := fmt.Sprintf(`CREATE DATABASE "%s" TEMPLATE "%s"`, clone, template)
        if err := exec.Command("psql", u.String(), "-X", "-v", "ON_ERROR_STOP=1", "-c", query).Run(); err != nil {
            t.Fatal("create fixture clone:", err)
        }
    }
    if mode == "fail" || mode == "interrupt" {
        dir := os.Getenv("FIXTURE_DIR")
        if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil { t.Fatal(err) }
        deadline := time.Now().Add(30*time.Second)
        for _, peer := range []string{"one", "two"} {
            for {
                if _, err := os.Stat(filepath.Join(dir, peer)); err == nil { break }
                if time.Now().After(deadline) { t.Fatal("packages did not overlap") }
                time.Sleep(10*time.Millisecond)
            }
        }
        if mode == "interrupt" { for { time.Sleep(time.Second) } }
        t.Fatal("deliberate failure in " + name)
    }
}
'''


class RunnerTest(unittest.TestCase):
    def setUp(self):
        if not os.environ.get("TEST_PG_URL"):
            self.fail("TEST_PG_URL must name the disposable test server")
        self.tmp = tempfile.TemporaryDirectory(prefix="x1-runner-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for rel in ("scripts/test-integration.sh", "scripts/lib/summarize-go-test.py"):
            dest = self.root / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / rel, dest)
        (self.root / "db").mkdir()
        (self.root / "db/schema.sql").write_text("-- empty fixture template\n")
        go_version = re.search(r"^go .+$", (ROOT / "go.mod").read_text(), re.MULTILINE)[0]
        (self.root / "go.mod").write_text(f"module runnerfixture\n\n{go_version}\n")
        for name in ("one", "two"):
            (self.root / name).mkdir()
            (self.root / name / "fixture_test.go").write_text(FIXTURE.replace("PACKAGE", name))
        self.ready = self.root / "ready"
        self.ready.mkdir()
        self.env = os.environ.copy()
        for key in ("TEST_WORKERS", "TEST_PKGS", "TEST_COUNT", "DB_DSN", "MIGRATE_TEST_DSN"):
            self.env.pop(key, None)
        self.env.update(FIXTURE_DIR=str(self.ready), GOWORK="off")
        self.command = ["bash", str(self.root / "scripts/test-integration.sh")]

    def assert_databases_removed(self, output):
        match = re.search(r"creating throwaway database (sh_it_\w+)", output)
        self.assertIsNotNone(match, output)
        name = match[1]
        result = subprocess.run([
            "psql", self.env["TEST_PG_URL"] + "/postgres?sslmode=disable", "-XAt",
            "-v", "ON_ERROR_STOP=1", "-c",
            f"SELECT datname FROM pg_database WHERE datname = '{name}' OR starts_with(datname, '{name}_t')",
        ], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, "could not check fixture cleanup")
        self.assertEqual(result.stdout.strip(), "", "runner left its databases behind")

    def run_fixture(self, mode="pass", workers=None, args=()):
        self.env["FIXTURE_MODE"] = mode
        if workers is not None:
            self.env["TEST_WORKERS"] = str(workers)
        result = subprocess.run(self.command + list(args), env=self.env,
                                capture_output=True, text=True, timeout=90)
        output = result.stdout + result.stderr
        self.assert_databases_removed(output)
        return result.returncode, output

    def test_serial_totals_and_forced_flags(self):
        code, output = self.run_fixture(workers=1, args=("-p", "8", "-count", "2", "-json=false"))
        self.assertEqual(code, 0, output)
        self.assertIn("forcing -p 1 -count=1 -json", output)
        self.assertIn("pass=4 fail=0 skip=0 (db-related-skip=0, other-skip=0)", output)

    def test_parallel_failures_in_both_packages(self):
        code, output = self.run_fixture("fail")
        self.assertNotEqual(code, 0, output)
        self.assertIn("forcing -p 3", output)
        for name in ("one", "two"):
            self.assertIn(f"FAIL runnerfixture/{name}", output)
            self.assertIn(f"--- FAIL: TestFixture (runnerfixture/{name}) ---", output)
            self.assertIn(f"deliberate failure in {name}", output)
        self.assertNotIn("packages did not overlap", output)
        self.assertIn("pass=2 fail=2 skip=0", output)

    def test_database_skips_fail(self):
        code, output = self.run_fixture("skip")
        self.assertNotEqual(code, 0, output)
        self.assertIn("pass=2 fail=0 skip=2 (db-related-skip=2, other-skip=0)", output)

    def test_invalid_workers_before_database_creation(self):
        for workers in ("0", "-1", "1.5", "three"):
            self.env["TEST_WORKERS"] = workers
            result = subprocess.run(self.command, env=self.env, capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("TEST_WORKERS must be a positive integer", result.stderr)
            self.assertNotIn("creating throwaway database", result.stdout)

    def test_interrupt_removes_both_clones(self):
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig):
                for path in self.ready.iterdir():
                    path.unlink()
                self.env["FIXTURE_MODE"] = "interrupt"
                proc = subprocess.Popen(self.command, env=self.env, stdout=subprocess.PIPE,
                                        stderr=subprocess.STDOUT, text=True)
                try:
                    deadline = time.monotonic() + 60
                    while len(list(self.ready.iterdir())) < 2 and time.monotonic() < deadline:
                        if proc.poll() is not None:
                            break
                        time.sleep(0.05)
                    self.assertEqual(len(list(self.ready.iterdir())), 2, "fixtures did not start")
                    proc.send_signal(sig)
                    output, _ = proc.communicate(timeout=15)
                    self.assertEqual(proc.returncode, 128 + sig, output)
                    self.assert_databases_removed(output)
                finally:
                    if proc.poll() is None:
                        proc.terminate()
                    proc.communicate(timeout=15)

    def test_interrupt_before_runner_pid_assignment(self):
        # Instrument only the copied runner: deliver the signal after launch,
        # before $! is recorded. A waiting child makes a leaked job observable.
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        wrapper = bin_dir / "go"
        wrapper.write_text('''#!/usr/bin/env bash
printf '%s' "$$" > "$LAUNCH_READY"
exec sleep 60
''')
        wrapper.chmod(0o755)
        ready_file = self.root / "launch-ready"
        pid_file = self.root / "launched-pid"
        self.env.update(PATH=str(bin_dir) + os.pathsep + self.env["PATH"],
                        LAUNCH_READY=str(ready_file), LAUNCH_PID=str(pid_file))
        runner = self.root / "scripts/test-integration.sh"
        source = runner.read_text()
        self.assertEqual(source.count("go_test_pid=$!"), 1)
        runner.write_text(source.replace("go_test_pid=$!", '''printf '%s' "$!" > "$LAUNCH_PID"
while [ ! -f "$LAUNCH_READY" ]; do sleep 0.01; done
kill -"$LAUNCH_SIGNAL" "$$"
go_test_pid=$!'''))
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig):
                self.env["LAUNCH_SIGNAL"] = str(int(sig))
                try:
                    # A leaked child inherits stdout; a file lets us inspect
                    # the runner's exit without waiting for that child as well.
                    with (self.root / "launch-output").open("w+") as log:
                        result = subprocess.run(self.command, env=self.env, stdout=log,
                                                stderr=subprocess.STDOUT, timeout=30)
                        log.seek(0)
                        output = log.read()
                    self.assertEqual(result.returncode, 128 + sig, output)
                    self.assertTrue(ready_file.exists(), output)
                    pid = int(pid_file.read_text())
                    with self.assertRaises(ProcessLookupError, msg="runner left its child alive"):
                        os.kill(pid, 0)
                    self.assert_databases_removed(output)
                finally:
                    # Also clean up when checking the old, leaking runner.
                    if pid_file.exists():
                        try:
                            os.killpg(int(pid_file.read_text()), signal.SIGTERM)
                        except ProcessLookupError:
                            pass
                        pid_file.unlink()
                    ready_file.unlink(missing_ok=True)

    def test_interrupt_during_database_creation(self):
        # Signal Bash after the real CREATE succeeds but before its foreground
        # psql command returns: the ownership flag has not yet been recorded.
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        wrapper = bin_dir / "psql"
        wrapper.write_text('''#!/usr/bin/env python3
import os
from pathlib import Path
import signal
import subprocess
import sys

result = subprocess.run([os.environ["REAL_PSQL"], *sys.argv[1:]])
query = sys.argv[-1]
if query.startswith('CREATE DATABASE "') and result.returncode == 0:
    Path(os.environ["CREATED_DB_FILE"]).write_text(query.split('"')[1])
    os.kill(os.getppid(), int(os.environ["SETUP_SIGNAL"]))
sys.exit(result.returncode)
''')
        wrapper.chmod(0o755)
        created_file = self.root / "created-db"
        self.env.update(REAL_PSQL=shutil.which("psql"),
                        PATH=str(bin_dir) + os.pathsep + self.env["PATH"],
                        CREATED_DB_FILE=str(created_file))
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig):
                self.env["SETUP_SIGNAL"] = str(int(sig))
                try:
                    result = subprocess.run(self.command, env=self.env, capture_output=True,
                                            text=True, timeout=30)
                    output = result.stdout + result.stderr
                    self.assertTrue(created_file.exists(), output)
                    self.assertEqual(result.returncode, 128 + sig, output)
                    self.assertNotIn("applying db/schema.sql", output)
                    self.assert_databases_removed(output)
                finally:
                    # Keep this regression safe even when run against the old,
                    # leaking runner: remove only the DB our wrapper created.
                    if created_file.exists():
                        name = created_file.read_text()
                        subprocess.run([
                            self.env["REAL_PSQL"], self.env["TEST_PG_URL"] + "/postgres?sslmode=disable",
                            "-X", "-v", "ON_ERROR_STOP=1", "-c",
                            f'DROP DATABASE IF EXISTS "{name}"',
                        ], capture_output=True, check=True)
                        created_file.unlink()


if __name__ == "__main__":
    unittest.main(verbosity=2)
