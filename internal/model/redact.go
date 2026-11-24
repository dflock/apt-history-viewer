package model

import (
	"path"
	"regexp"
	"strings"
)

// Redact removes what identifies the people using a machine, so a report can
// be shared (--redact): usernames and uids, command-line arguments, and file
// paths outside the system log directories (an input such as
// /home/alice/logs.tgz names its owner). The host name is kept; --host
// replaces it.
func (r *Report) Redact() {
	r.Redacted = true
	in := r.Input
	r.Input = redactPath(in, in)
	for i, f := range r.Files {
		r.Files[i] = redactPath(f, in)
	}
	for i := range r.Transactions {
		t := &r.Transactions[i]
		t.User, t.UID = "", nil
		t.Commandline = redactCommand(t.Commandline)
		t.File = redactPath(t.File, in)
		t.Error = redactText(t.Error, in)
	}
	for i := range r.Warnings {
		w := &r.Warnings[i]
		w.File = redactPath(w.File, in)
		w.Msg = redactText(w.Msg, in)
	}
}

// redactCommand keeps the program and its subcommand: "apt install foo bar"
// becomes "apt install …", "/usr/bin/python3 /usr/bin/unattended-upgrade -d"
// becomes "unattended-upgrade …".
func redactCommand(cmd string) string {
	tool := toolOf(cmd)
	if tool == "" {
		return ""
	}
	words := strings.Fields(cmd)
	i := 0
	for i < len(words) && path.Base(words[i]) != tool {
		i++
	}
	kept := []string{tool}
	rest := words[min(i+1, len(words)):]
	for j, w := range rest {
		if strings.HasPrefix(w, "-") {
			continue
		}
		if verbRe.MatchString(w) {
			kept = append(kept, w)
			rest = append(rest[:j:j], rest[j+1:]...)
		}
		break
	}
	if len(rest) > 0 {
		kept = append(kept, "…")
	}
	return strings.Join(kept, " ")
}

// verbRe matches subcommands such as install, dist-upgrade or safe-upgrade.
var verbRe = regexp.MustCompile(`^[a-z][a-z-]*$`)

// systemDirs are where logs live on any machine; paths under them say nothing
// about the people using it.
var systemDirs = []string{"/var/log/", "/var/lib/", "/etc/", "/usr/", "/bin/", "/sbin/", "/lib/"}

// redactPath shortens p, read from input in: paths inside the input keep their
// part below it (behind the input's base name), system log paths are kept,
// and anything else is cut to its base name.
func redactPath(p, in string) string {
	switch {
	case p == "" || in == "":
		return p
	case p == in:
		if in == "/" {
			return in
		}
		return path.Base(strings.TrimRight(in, "/"))
	case in != "/":
		root := strings.TrimRight(in, "/")
		if rest, ok := strings.CutPrefix(p, root); ok && (rest == "" || rest[0] == '/' || rest[0] == ':') {
			return path.Base(root) + rest
		}
	}
	for _, d := range systemDirs {
		if strings.HasPrefix(p, d) {
			return p
		}
	}
	return path.Base(p)
}

var (
	quotedRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	pathRe   = regexp.MustCompile(`(?:^|[\s(=])/[^\s:"'()]+`)
)

// redactText removes quoted log text (a bad line can be a Requested-By or
// Commandline line) and shortens paths in a message.
func redactText(s, in string) string {
	if s == "" {
		return s
	}
	s = quotedRe.ReplaceAllString(s, `"…"`)
	if in != "/" && in != "" {
		root := strings.TrimRight(in, "/")
		s = strings.ReplaceAll(s, root, path.Base(root))
	}
	return pathRe.ReplaceAllStringFunc(s, func(m string) string {
		if m[0] == '/' {
			return redactPath(m, "/")
		}
		return m[:1] + redactPath(m[1:], "/")
	})
}
