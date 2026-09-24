package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForEvents(t *testing.T, path string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			events := strings.FieldsFunc(strings.TrimSpace(string(data)), func(r rune) bool { return r == '\n' })
			if len(events) >= n {
				return events
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events in %s", n, path)
	return nil
}

func TestWatchReplacement(t *testing.T) {
	dir := t.TempDir()
	watcher := filepath.Join(dir, "watch")
	build := exec.Command("go", "build", "-o", watcher, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build watcher: %v\n%s", err, output)
	}
	published := filepath.Join(dir, "server")
	build = exec.Command("go", "build", "-o", published, "./testdata/child")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test child: %v\n%s", err, output)
	}
	eventsPath := filepath.Join(dir, "events")
	outputPath := filepath.Join(dir, "watch.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd := exec.Command(watcher, published)
	cmd.Env = append(os.Environ(), "INFRAPAD_WATCH_TEST_EVENTS_FILE="+eventsPath)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	running := true
	defer func() {
		if running {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-finished
			}
		}
		if t.Failed() {
			logData, _ := os.ReadFile(outputPath)
			t.Logf("watcher output:\n%s", logData)
		}
	}()

	first := waitForEvents(t, eventsPath, 1)
	if !strings.HasPrefix(first[0], "start ") {
		t.Fatalf("first event: %v", first)
	}

	// A failed compile never publishes a new executable and cannot interrupt the child.
	failed := exec.Command("go", "build", "-o", filepath.Join(dir, "staging"), "./nonexistent-package")
	if err := failed.Run(); err == nil {
		t.Fatal("expected build failure")
	}
	before, err := os.Stat(published)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * pollInterval)
	if data, err := os.ReadFile(eventsPath); err != nil || strings.TrimSpace(string(data)) != first[0] {
		t.Fatalf("failed build interrupted child: %q (%v)", data, err)
	}

	staging := filepath.Join(dir, "next")
	copyExecutable(t, published, staging)
	if err := os.Rename(staging, published); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(published)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("binary was not replaced")
	}
	replaced := waitForEvents(t, eventsPath, 3)
	if replaced[1] != strings.Replace(first[0], "start", "stop", 1) || !strings.HasPrefix(replaced[2], "start ") || replaced[2] == first[0] {
		t.Fatalf("restart overlapped or missed replacement: %v", replaced)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	stopped := waitForEvents(t, eventsPath, 4)
	if stopped[3] != strings.Replace(replaced[2], "start", "stop", 1) {
		t.Fatalf("child did not stop cleanly: %v", stopped)
	}
	select {
	case err := <-finished:
		running = false
		if err != nil {
			t.Fatalf("watcher shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not exit after child shutdown")
	}
}
