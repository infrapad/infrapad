package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// runChild simulates a server that needs time to release its ports on shutdown.
func runChild() error {
	info, err := os.Stat("/proc/self/exe")
	if err != nil {
		return err
	}
	id := fmt.Sprint(info.Sys().(*syscall.Stat_t).Ino)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	eventsFile := os.Getenv("INFRAPAD_WATCH_TEST_EVENTS_FILE")
	if err := appendEvent(eventsFile, "start "+id); err != nil {
		return err
	}
	<-signals
	// Make an overlapping restart visible to the parent test.
	time.Sleep(350 * time.Millisecond)
	return appendEvent(eventsFile, "stop "+id)
}

func appendEvent(path, event string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, event); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func main() {
	if err := runChild(); err != nil {
		log.Fatal(err)
	}
}
