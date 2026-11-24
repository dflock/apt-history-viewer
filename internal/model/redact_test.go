package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dflock/apt-history-viewer/internal/diag"
)

func TestRedactCommand(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"apt install xdotool xvfb", "apt install …"},
		{"apt upgrade", "apt upgrade"},
		{"apt-get -y dist-upgrade", "apt-get dist-upgrade …"},
		{"/usr/bin/unattended-upgrade", "unattended-upgrade"},
		{"/usr/bin/python3 /usr/bin/unattended-upgrade -d", "unattended-upgrade …"},
		{"aptitude --full-resolver -q safe-upgrade", "aptitude safe-upgrade …"},
		{"apt install ./alice-tool_1.0_amd64.deb", "apt install …"},
		{"dpkg -i /home/alice/Downloads/x.deb", "dpkg …"},
		{"/usr/sbin/synaptic", "synaptic"},
		{"", ""},
	} {
		if got := redactCommand(c.in); got != c.want {
			t.Errorf("redactCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactPath(t *testing.T) {
	for _, c := range []struct{ p, in, want string }{
		{"/var/log/apt/history.log", "/", "/var/log/apt/history.log"},
		{"/home/alice/logs/history.log", "/", "history.log"}, // --apt-dir override
		{"/home/alice/box.tgz:var/log/dpkg.log", "/home/alice/box.tgz", "box.tgz:var/log/dpkg.log"},
		{"/home/alice/box/var/log/dpkg.log", "/home/alice/box/", "box/var/log/dpkg.log"},
		{"/home/alice/box.tgz", "/home/alice/box.tgz", "box.tgz"},
		{"/", "/", "/"},
	} {
		if got := redactPath(c.p, c.in); got != c.want {
			t.Errorf("redactPath(%q, %q) = %q, want %q", c.p, c.in, got, c.want)
		}
	}
}

func TestRedactReport(t *testing.T) {
	uid := 1000
	in := "/home/alice/box.tgz"
	r := &Report{
		Input: in,
		Files: []string{in + ":var/log/apt/history.log"},
		Transactions: []Transaction{{
			Commandline: "apt install ./secret-alice.deb", User: "alice", UID: &uid,
			File: in + ":var/log/apt/history.log", Error: "Sub-process /usr/bin/dpkg returned an error code (1)",
		}},
		Warnings: []diag.Warning{
			{File: in + ":var/log/apt/history.log", Line: 3, Msg: `unrecognised line: "Requested-By: alice (1000)"`},
			{File: "/home/alice/apt", Msg: "cannot open: open /home/alice/apt/history.log: permission denied"},
		},
	}
	r.Redact()
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "alice") || strings.Contains(string(b), "1000") {
		t.Errorf("redacted report still names the user: %s", b)
	}
	if !r.Redacted {
		t.Error("Redacted not set")
	}
	if got := r.Transactions[0].Error; got != "Sub-process /usr/bin/dpkg returned an error code (1)" {
		t.Errorf("system path in error changed: %q", got)
	}
	if got := r.Warnings[1].Msg; got != "cannot open: open history.log: permission denied" {
		t.Errorf("warning = %q", got)
	}
}
