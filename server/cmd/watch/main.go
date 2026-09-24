// Command watch restarts a server only when its published executable is replaced.
//
// Using explicit file over task --watch feature, as the latter was observed to
// start a replacement before the old server's graceful shutdown completed.
// Polling the atomically published binary lets us wait for port release before
// starting another process.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	pollInterval    = 200 * time.Millisecond
	shutdownTimeout = 12 * time.Second // The server allows up to 10 seconds for HTTP shutdown.
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: infrapad-server-watch <binary> [arguments...]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := watch(ctx, os.Args[1], os.Args[2:], pollInterval); err != nil {
		log.Fatal(err)
	}
}

func watch(ctx context.Context, binary string, args []string, interval time.Duration) error {
	published, err := os.Stat(binary)
	if err != nil {
		return fmt.Errorf("inspect server binary %s: %w", binary, err)
	}
	if !published.Mode().IsRegular() {
		return fmt.Errorf("server binary %s is not a regular file", binary)
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	child, exited, err := start(binary, args)
	if err != nil {
		return err
	}
	// A trade-off of small delay and polling overhead over fsnotify dependency.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return stopChild(child, exited)
		case err := <-exited:
			return fmt.Errorf("server exited unexpectedly: %v", err)
		case <-ticker.C:
			latest, err := os.Stat(binary)
			if err == nil && !latest.Mode().IsRegular() {
				err = fmt.Errorf("not a regular file")
			}
			if err != nil {
				_ = stopChild(child, exited)
				return fmt.Errorf("inspect server binary %s: %w", binary, err)
			}
			if os.SameFile(published, latest) {
				continue
			}
			select {
			case err := <-exited:
				return fmt.Errorf("server exited unexpectedly before replacement: %v", err)
			default:
			}

			log.Printf("server binary replaced; stopping old server")
			if err := stopChild(child, exited); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			// Coalesce any additional replacements made during graceful shutdown.
			published, err = os.Stat(binary)
			if err != nil {
				return fmt.Errorf("inspect replacement server binary %s: %w", binary, err)
			}
			if !published.Mode().IsRegular() {
				return fmt.Errorf("replacement server binary %s is not a regular file", binary)
			}
			child, exited, err = start(binary, args)
			if err != nil {
				return err
			}
		}
	}
}

func start(binary string, args []string) (*exec.Cmd, <-chan error, error) {
	child := exec.Command(binary, args...)
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Stdin = os.Stdin
	if err := child.Start(); err != nil {
		return nil, nil, fmt.Errorf("start server %s: %w", binary, err)
	}
	log.Printf("started server (pid %d)", child.Process.Pid)
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	return child, exited, nil
}

func stopChild(child *exec.Cmd, exited <-chan error) error {
	// A child that exited on its own is still reaped by the Wait goroutine.
	select {
	case <-exited:
		return nil
	default:
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = child.Process.Kill()
		<-exited
		return fmt.Errorf("signal server (pid %d): %w", child.Process.Pid, err)
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-exited:
		return nil
	case <-timer.C:
		log.Printf("server (pid %d) did not stop gracefully; killing it", child.Process.Pid)
		if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill server (pid %d): %w", child.Process.Pid, err)
		}
		<-exited
		return nil
	}
}
