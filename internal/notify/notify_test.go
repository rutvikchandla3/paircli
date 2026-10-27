package notify

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func lookup(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

func TestMacOSUsesArgvSafeOsascript(t *testing.T) {
	got := command("darwin", lookup("osascript"), `Title "quoted"`, `msg"; do shell script "x`)
	want := []string{"/usr/bin/osascript", "-e", macScript, "--", `Title "quoted"`, `msg"; do shell script "x`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestLinuxUsesNotifySend(t *testing.T) {
	got := command("linux", lookup("notify-send"), "Pair review ready", "PR #5")
	want := []string{"/usr/bin/notify-send", "--app-name", "Pair", "Pair review ready", "PR #5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestNoNotifierIsANoop(t *testing.T) {
	if command("darwin", lookup(), "t", "m") != nil || command("linux", lookup(), "t", "m") != nil {
		t.Error("a missing notifier binary produced a command")
	}
	if command("windows", lookup("osascript", "notify-send"), "t", "m") != nil {
		t.Error("an unsupported platform produced a command")
	}
}

func TestSendReportsAFailingNotifierAsNotShown(t *testing.T) {
	old := run
	defer func() { run = old }()
	run = func([]string) error { return errors.New("boom") }
	if Send("t", "m") {
		t.Error("Send reported success for a failing notifier")
	}
}
