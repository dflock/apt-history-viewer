package model

import (
	"cmp"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dflock/apt-history-viewer/internal/apthistory"
	"github.com/dflock/apt-history-viewer/internal/aptitudelog"
	"github.com/dflock/apt-history-viewer/internal/collect"
	"github.com/dflock/apt-history-viewer/internal/dpkglog"
)

const (
	// slack around a transaction's [Start, End] within which a dpkg run is
	// still counted as part of it (DESIGN.md §5.3). Both logs use whole seconds.
	slack = 2 * time.Second
	// aptitudeWindow: aptitude writes its report 0–1 s before apt's Start-Date (§3.7).
	aptitudeWindow = 2 * time.Second
	// openEnded caps the matching window of a transaction with no End-Date.
	openEnded = time.Hour
	// dpkgGap: unmatched dpkg runs this close together are one invocation
	// (dpkg -i logs an unpack run and a configure run).
	dpkgGap = 2 * time.Second
)

// brokenStates are dpkg states that mean a package was left half-done when
// they are the last state recorded for it in a transaction.
var brokenStates = map[string]bool{"half-installed": true, "half-configured": true, "unpacked": true}

// Build correlates the raw parse into a Report. now is the generation time.
func Build(res *collect.Result, now time.Time) *Report {
	b := &builder{res: res, allArch: map[string]bool{}}
	b.findAllArch()

	r := &Report{
		Input:        res.Input,
		Host:         res.Host,
		TZ:           res.TZ,
		Generated:    now,
		HasBaseline:  res.DpkgStatus != nil,
		Files:        res.Files,
		Warnings:     res.Warnings,
		Transactions: []Transaction{},
		Packages:     []Package{},
	}
	r.From, r.To = res.Span()
	for _, s := range res.AptHistory {
		if r.AptFrom.IsZero() || s.Start.Before(r.AptFrom) {
			r.AptFrom = s.Start
		}
	}
	for _, run := range res.DpkgRuns {
		if r.DpkgFrom.IsZero() || run.Start.Before(r.DpkgFrom) {
			r.DpkgFrom = run.Start
		}
	}

	txs := b.aptTransactions()
	txs = append(txs, b.matchRuns(txs)...)
	slices.SortStableFunc(txs, func(a, b Transaction) int { return a.Start.Compare(b.Start) })
	for i := range txs {
		txs[i].ID = i
	}
	r.Transactions = txs
	r.Packages = b.packages(txs)
	return r
}

type builder struct {
	res *collect.Result
	// allArch holds names dpkg reports as Architecture: all. history.log and
	// extended_states write those with the native arch instead (DESIGN.md §2).
	allArch map[string]bool
	// runsOf[i] are the dpkg runs matched to apt transaction i, in time order.
	runsOf map[int][]*dpkglog.Run
}

func (b *builder) findAllArch() {
	for _, e := range b.res.DpkgStatus {
		if e.Arch == "all" {
			b.allArch[e.Name] = true
		}
	}
	for _, run := range b.res.DpkgRuns {
		for _, ev := range run.Events {
			if ev.Arch == "all" {
				b.allArch[ev.Name] = true
			}
		}
	}
}

func (b *builder) arch(name, arch string) string {
	if b.allArch[name] {
		return "all"
	}
	return arch
}

// aptTransactions turns history.log stanzas into transactions (§5.1, §5.6, §5.7),
// sorted by Start. Changes are not yet timed; matchRuns does that.
func (b *builder) aptTransactions() []Transaction {
	stanzas := slices.Clone(b.res.AptHistory)
	slices.SortStableFunc(stanzas, func(x, y apthistory.Stanza) int { return x.Start.Compare(y.Start) })
	reports := slices.Clone(b.res.AptitudeReports)
	slices.SortFunc(reports, func(x, y aptitudelog.Report) int { return x.Time.Compare(y.Time) })

	txs := make([]Transaction, 0, len(stanzas))
	for _, s := range stanzas {
		t := Transaction{
			Start:       s.Start,
			End:         s.End,
			Commandline: s.Commandline,
			User:        s.RequestedBy,
			Error:       s.Error,
			Incomplete:  s.End.IsZero(),
			Sources:     []string{"apt"},
			File:        s.File,
			Line:        s.Line,
			Changes:     []Change{},
		}
		if s.HasUID {
			t.UID = &s.UID
		}
		t.Origin, t.Tool = classify(&s, reports)
		t.Manual = t.Origin.Manual()
		named := namedPackages(t.Tool, s.Commandline)
		for _, it := range s.Items {
			t.Changes = append(t.Changes, Change{
				Name:       it.Name,
				Arch:       b.arch(it.Name, it.Arch),
				Action:     Action(it.Action),
				Old:        it.Old,
				New:        it.New,
				Dependency: it.Automatic,
				Requested:  !it.Automatic,
				Named:      named[it.Name],
				At:         s.Start,
			})
		}
		txs = append(txs, t)
	}
	return txs
}

// classify applies DESIGN.md §5.6.
func classify(s *apthistory.Stanza, reports []aptitudelog.Report) (Origin, string) {
	tool := toolOf(s.Commandline)
	switch {
	case tool == "unattended-upgrade" || strings.Contains(s.Commandline, "unattended-upgrade"):
		return OriginUnattended, "unattended-upgrade"
	case tool == "synaptic":
		return OriginUserGUI, tool
	case s.Commandline == "" && s.RequestedBy != "":
		if aptitudeNear(reports, s.Start) {
			return OriginUserCLI, "aptitude"
		}
		return OriginUserGUI, "" // probably PackageKit: "user, tool unknown"
	case s.RequestedBy != "":
		return OriginUserCLI, tool
	default:
		return OriginRoot, tool
	}
}

// aptitudeNear reports whether an aptitude report falls in [start−2s, start].
func aptitudeNear(reports []aptitudelog.Report, start time.Time) bool {
	i := sort.Search(len(reports), func(i int) bool { return !reports[i].Time.Before(start.Add(-aptitudeWindow)) })
	return i < len(reports) && !reports[i].Time.After(start)
}

var interpreters = map[string]bool{
	"env": true, "sudo": true, "nice": true, "ionice": true, "nohup": true,
	"sh": true, "bash": true, "dash": true, "perl": true,
}

func isInterpreter(base string) bool {
	return interpreters[base] || strings.HasPrefix(base, "python")
}

// toolOf returns the program a command line ran, skipping wrappers and
// interpreters: "/usr/bin/python3 /usr/bin/unattended-upgrade" → unattended-upgrade.
func toolOf(cmd string) string {
	words := strings.Fields(cmd)
	for i := 0; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") || strings.Contains(w, "=") {
			continue // interpreter option or env assignment
		}
		base := path.Base(w)
		if isInterpreter(base) && i < len(words)-1 {
			continue
		}
		return base
	}
	return ""
}

// namedPackages returns package names that appear as arguments on an
// apt/apt-get/aptitude command line (§5.7's cross-check).
func namedPackages(tool, cmd string) map[string]bool {
	if tool != "apt" && tool != "apt-get" && tool != "aptitude" {
		return nil
	}
	words := strings.Fields(cmd)
	i := slices.IndexFunc(words, func(w string) bool { return path.Base(w) == tool })
	if i < 0 {
		return nil
	}
	names := map[string]bool{}
	seenVerb := false
	for _, w := range words[i+1:] {
		if strings.HasPrefix(w, "-") {
			continue
		}
		if !seenVerb {
			seenVerb = true // install, remove, upgrade, …
			continue
		}
		// pkg=version, pkg/release, pkg:arch; aptitude's pkg+ / pkg- / pkg_ suffixes.
		w, _, _ = strings.Cut(w, "=")
		w, _, _ = strings.Cut(w, "/")
		w, _, _ = strings.Cut(w, ":")
		names[w] = true
		if t := strings.TrimRight(w, "+-_&"); t != "" {
			names[t] = true
		}
	}
	return names
}

// matchRuns attaches dpkg runs to apt transactions (§5.3–5.5), times their
// changes from dpkg events, flags half-done packages, and returns synthetic
// transactions for runs apt didn't record. txs must be sorted by Start.
func (b *builder) matchRuns(txs []Transaction) []Transaction {
	runs := make([]*dpkglog.Run, len(b.res.DpkgRuns))
	for i := range b.res.DpkgRuns {
		runs[i] = &b.res.DpkgRuns[i]
	}
	slices.SortStableFunc(runs, func(x, y *dpkglog.Run) int { return x.Start.Compare(y.Start) })

	// Matching window per transaction. A missing End-Date leaves it open until
	// the next transaction starts (at most openEnded).
	ends := make([]time.Time, len(txs))
	var maxDur time.Duration
	for i, t := range txs {
		e := t.End
		if e.IsZero() || e.Before(t.Start) {
			e = t.Start.Add(openEnded)
			if i+1 < len(txs) && txs[i+1].Start.Before(e) {
				e = later(t.Start, txs[i+1].Start.Add(-slack-time.Second))
			}
		}
		ends[i] = e
		maxDur = max(maxDur, e.Sub(t.Start))
	}

	b.runsOf = map[int][]*dpkglog.Run{}
	var unmatched []*dpkglog.Run
	for _, run := range runs {
		if i := bestTx(txs, ends, maxDur, run.Start); i >= 0 {
			b.runsOf[i] = append(b.runsOf[i], run)
		} else {
			unmatched = append(unmatched, run)
		}
	}

	for i := range txs {
		rs := b.runsOf[i]
		if len(rs) == 0 {
			continue
		}
		t := &txs[i]
		t.Sources = append(t.Sources, "dpkg")
		b.timeChanges(t, rs)
		if b.halfDone(rs) {
			t.Incomplete = true
		}
		if t.End.IsZero() {
			t.End = t.Start
			for _, r := range rs {
				t.End = later(t.End, r.End)
			}
		}
	}
	for i := range txs {
		if txs[i].End.IsZero() {
			txs[i].End = txs[i].Start
		}
	}
	return b.dpkgTransactions(unmatched)
}

// bestTx picks the transaction a run starting at at belongs to, or −1.
// A run inside [Start, End] wins over one only within the slack; among
// equals, the latest-starting transaction wins.
func bestTx(txs []Transaction, ends []time.Time, maxDur time.Duration, at time.Time) int {
	// First transaction whose Start−slack is after at; candidates are before it.
	hi := sort.Search(len(txs), func(i int) bool { return txs[i].Start.Add(-slack).After(at) })
	best, bestDist := -1, time.Duration(1<<62)
	for j := hi - 1; j >= 0 && !txs[j].Start.Before(at.Add(-maxDur-slack)); j-- {
		start, end := txs[j].Start, ends[j]
		var dist time.Duration
		switch {
		case at.Before(start):
			dist = start.Sub(at)
		case at.After(end):
			dist = at.Sub(end)
		}
		if dist > slack {
			continue
		}
		if dist < bestDist {
			best, bestDist = j, dist
		}
	}
	return best
}

// dpkgVerb maps an apt action to the dpkg.log verb that performs it.
func dpkgVerb(a Action) string {
	switch a {
	case Upgrade, Downgrade, Reinstall:
		return "upgrade"
	default:
		return string(a)
	}
}

func archOK(a, b string) bool { return a == b || a == "" || b == "" || a == "all" || b == "all" }

// timeChanges sets each change's At from the first matching dpkg event.
func (b *builder) timeChanges(t *Transaction, runs []*dpkglog.Run) {
	type ev struct {
		*dpkglog.Event
		used bool
	}
	byName := map[string][]*ev{}
	for _, r := range runs {
		for i := range r.Events {
			e := &r.Events[i]
			byName[e.Name] = append(byName[e.Name], &ev{Event: e})
		}
	}
	for i := range t.Changes {
		c := &t.Changes[i]
		var pick *ev
		for _, e := range byName[c.Name] {
			if e.used || !archOK(c.Arch, b.arch(e.Name, e.Arch)) {
				continue
			}
			if e.Action == dpkgVerb(c.Action) {
				pick = e
				break
			}
			if pick == nil {
				pick = e // fallback: any event for the package (purge logs remove first)
			}
		}
		if pick != nil {
			pick.used = true
			c.At = pick.Time
		}
	}
}

// halfDone reports whether any package's last state across runs is broken.
// States are merged per package name with normalised arch: when a package
// switches arch (linux-firmware went amd64 → all), dpkg leaves the old
// instance "half-installed" as the new one replaces it, so within a run a
// good state for the same name wins.
func (b *builder) halfDone(runs []*dpkglog.Run) bool {
	final := map[string]string{}
	for _, r := range runs {
		run := map[string]string{}
		for k, v := range r.FinalStatus {
			name, arch, _ := strings.Cut(k, ":")
			key := name + ":" + b.arch(name, arch)
			if prev, ok := run[key]; !ok || brokenStates[prev] {
				run[key] = v
			}
		}
		for k, v := range run {
			final[k] = v
		}
	}
	for _, v := range final {
		if brokenStates[v] {
			return true
		}
	}
	return false
}

// dpkgTransactions groups unmatched runs into one transaction per dpkg
// invocation (§5.5). Groups without package changes (trigger or configure
// runs) are dropped.
func (b *builder) dpkgTransactions(runs []*dpkglog.Run) []Transaction {
	var out []Transaction
	for i := 0; i < len(runs); {
		j := i + 1
		end := runs[i].End
		for j < len(runs) && runs[j].Start.Sub(end) <= dpkgGap {
			end = later(end, runs[j].End)
			j++
		}
		group := runs[i:j]
		i = j

		t := Transaction{
			Start:      group[0].Start,
			End:        end,
			Origin:     OriginDpkg,
			Manual:     OriginDpkg.Manual(),
			Tool:       "dpkg",
			Incomplete: b.halfDone(group),
			Sources:    []string{"dpkg"},
			File:       group[0].File,
			Line:       group[0].Line,
			Changes:    b.dpkgChanges(group),
		}
		if len(t.Changes) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// dpkgChanges merges the events of a group of runs into one change per
// package: first old version, last new version; remove then purge = purge.
func (b *builder) dpkgChanges(runs []*dpkglog.Run) []Change {
	var out []Change
	idx := map[string]int{}
	for _, r := range runs {
		for _, e := range r.Events {
			arch := b.arch(e.Name, e.Arch)
			k := e.Name + ":" + arch
			act := Action(e.Action)
			if act == Upgrade && e.Old == e.New {
				act = Reinstall
			}
			i, seen := idx[k]
			if !seen {
				idx[k] = len(out)
				out = append(out, Change{
					Name: e.Name, Arch: arch, Action: act, Old: e.Old, New: e.New,
					Requested: true, At: e.Time,
				})
				continue
			}
			c := &out[i]
			if e.New != "" {
				c.New = e.New
			}
			if act == Purge || act == Remove {
				c.Action, c.New = act, ""
			}
		}
	}
	return out
}

// packages builds the per-package view (§5.8).
func (b *builder) packages(txs []Transaction) []Package {
	byKey := map[string]*Package{}
	get := func(name, arch string) *Package {
		k := name + ":" + arch
		p := byKey[k]
		if p == nil {
			p = &Package{Name: name, Arch: arch, Events: []EventRef{}}
			byKey[k] = p
		}
		return p
	}
	for ti, t := range txs {
		for ci, c := range t.Changes {
			p := get(c.Name, c.Arch)
			p.Events = append(p.Events, EventRef{Tx: ti, Change: ci})
		}
	}
	for _, e := range b.res.DpkgStatus {
		p := get(e.Name, b.arch(e.Name, e.Arch))
		if e.Installed() {
			p.Installed = e.Version
		} else {
			p.State = e.State
		}
	}
	for _, a := range b.res.AutoInstalled {
		if p := byKey[a.Name+":"+b.arch(a.Name, a.Arch)]; p != nil {
			p.AutoNow = true
		}
	}

	out := make([]Package, 0, len(byKey))
	for _, p := range byKey {
		if len(p.Events) == 0 {
			p.PreHistory = p.Installed != "" || p.State != ""
		} else {
			first := txs[p.Events[0].Tx].Changes[p.Events[0].Change].Action
			p.PreHistory = first != Install
		}
		out = append(out, *p)
	}
	slices.SortFunc(out, func(x, y Package) int {
		return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.Arch, y.Arch))
	})
	return out
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
