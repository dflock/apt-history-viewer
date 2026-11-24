// Package model turns the raw parse (collect.Result) into the Report that the
// HTML view and --json show: transactions with an origin, per-change dpkg
// timestamps, and a per-package history (DESIGN.md §4, §5).
package model

import (
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
)

// Origin says who or what started a transaction. Each origin gets its own
// lane in the timeline.
type Origin string

const (
	OriginUserCLI    Origin = "user-cli"   // Requested-By + Commandline (apt, apt-get, aptitude)
	OriginUserGUI    Origin = "user-gui"   // synaptic, or Requested-By with no Commandline and no aptitude report
	OriginUnattended Origin = "unattended" // unattended-upgrade
	OriginRoot       Origin = "root"       // Commandline but no Requested-By (root shell, scripts)
	OriginDpkg       Origin = "dpkg"       // dpkg runs with no apt transaction (dpkg -i, …)
)

// Manual reports whether a person started transactions of this origin.
func (o Origin) Manual() bool { return o != OriginUnattended }

// Action is what happened to a package.
type Action string

const (
	Install   Action = "install"
	Upgrade   Action = "upgrade"
	Downgrade Action = "downgrade"
	Reinstall Action = "reinstall"
	Remove    Action = "remove"
	Purge     Action = "purge"
)

// Transaction is one apt run, or a group of dpkg runs apt didn't record.
type Transaction struct {
	ID          int       `json:"id"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Origin      Origin    `json:"origin"`
	Manual      bool      `json:"manual"`                // Origin.Manual(), for the view
	Tool        string    `json:"tool,omitempty"`        // apt, apt-get, aptitude, synaptic, unattended-upgrade, dpkg; "" = unknown
	Commandline string    `json:"commandline,omitempty"` //
	User        string    `json:"user,omitempty"`        // from Requested-By
	UID         *int      `json:"uid,omitempty"`         //
	Error       string    `json:"error,omitempty"`       //
	Incomplete  bool      `json:"incomplete,omitempty"`  // no End-Date, or dpkg left a package half-done
	Changes     []Change  `json:"changes"`               //
	Sources     []string  `json:"sources"`               // "apt", "dpkg"
	File        string    `json:"file,omitempty"`        // where it was read from, for warnings and debugging
	Line        int       `json:"line,omitempty"`        //
}

// Change is one package changed by a transaction.
type Change struct {
	Name   string `json:"name"`
	Arch   string `json:"arch,omitempty"` // normalised: "all" when dpkg says so, even if apt said amd64
	Action Action `json:"action"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
	// Dependency: apt marked it "automatic" (pulled in by another package).
	Dependency bool `json:"dependency,omitempty"`
	// Requested: what the person asked for, as opposed to dependencies.
	// Every non-Install action counts as requested; see DESIGN.md §5.7.
	Requested bool `json:"requested,omitempty"`
	// Named: the package appears on the command line.
	Named bool `json:"named,omitempty"`
	// At: dpkg's timestamp for the change when it was matched, else the
	// transaction's Start.
	At time.Time `json:"at"`
}

// EventRef points at one Change: Transactions[Tx].Changes[Change].
type EventRef struct {
	Tx     int `json:"tx"`
	Change int `json:"change"`
}

// Package is the derived per-package view.
type Package struct {
	Name      string `json:"name"`
	Arch      string `json:"arch,omitempty"`
	Installed string `json:"installed,omitempty"` // current version, "" if not installed
	State     string `json:"state,omitempty"`     // dpkg state when not fully installed (config-files, …)
	AutoNow   bool   `json:"autoNow,omitempty"`   // currently marked automatically installed
	// PreHistory: present before the logs begin (installed with no install
	// event, or first seen being upgraded or removed).
	PreHistory bool       `json:"preHistory,omitempty"`
	Events     []EventRef `json:"events"`
}

// Report is everything the view needs for one machine.
type Report struct {
	Input     string    `json:"input"`
	Host      string    `json:"host"`
	TZ        string    `json:"tz"`
	Generated time.Time `json:"generated"`
	From      time.Time `json:"from,omitzero"` // earliest time in any log
	To        time.Time `json:"to,omitzero"`   // latest time in any log
	// AptFrom/DpkgFrom: where each log starts. dpkg runs before AptFrom can't be
	// told apart from apt runs whose history.log entry has rotated away.
	AptFrom      time.Time      `json:"aptFrom,omitzero"`
	DpkgFrom     time.Time      `json:"dpkgFrom,omitzero"`
	Redacted     bool           `json:"redacted,omitempty"` // --redact: no users, arguments or private paths
	HasBaseline  bool           `json:"hasBaseline"`        // dpkg/status was read, so Package.Installed/PreHistory mean something
	Transactions []Transaction  `json:"transactions"`
	Packages     []Package      `json:"packages"`
	Files        []string       `json:"files"`
	Warnings     []diag.Warning `json:"warnings"`
}
