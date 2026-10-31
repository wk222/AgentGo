package governance

import (
	"regexp"
	"strings"
)

// Reading a shell command for deletes. execute_bash always waits for the
// user's approval, so this is not what stops a delete from running. It does two
// things the approval alone cannot: it refuses outright a recursive delete of a
// place nobody means to empty (a drive root, a user profile, the system
// folders), which a tired reviewer can approve by mistake, and it names the
// delete and its targets in the approval prompt instead of leaving the user to
// pick it out of the command text.
//
// This is a reader, not a sandbox. A delete hidden behind an encoded command, an
// eval, a variable or a script file is invisible to it and still goes through
// approval like any other command.

type shellDelete struct {
	Program   string
	Operands  []string
	Recursive bool
}

func (d shellDelete) String() string {
	return strings.TrimSpace(d.Program + " " + strings.Join(d.Operands, " "))
}

type shellToken struct {
	text string
	op   bool // a command separator: && || ; | & newline
}

func tokenizeShell(cmd string) []shellToken {
	var out []shellToken
	var cur strings.Builder
	has := false
	var quote rune
	flush := func() {
		if has {
			out = append(out, shellToken{text: cur.String()})
		}
		cur.Reset()
		has = false
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		case r == '\n' || r == ';':
			flush()
			out = append(out, shellToken{op: true})
		case r == '&' || r == '|':
			flush()
			if i+1 < len(runes) && runes[i+1] == r {
				i++
			}
			out = append(out, shellToken{op: true})
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

var (
	deletePrograms = map[string]bool{"rm": true, "del": true, "erase": true, "rd": true, "rmdir": true, "remove-item": true, "ri": true}
	// Programs that run what follows as a command, so the delete is the next word.
	transparentPrefix = map[string]bool{"sudo": true, "xargs": true, "command": true, "exec": true, "env": true, "nohup": true, "call": true, "start": true, "time": true}
	// Programs that take a command string; the string is read as a command too.
	shellWrappers = map[string]bool{"cmd": true, "powershell": true, "pwsh": true, "bash": true, "sh": true, "zsh": true, "wsl": true}
	wrapperFlags  = map[string]bool{"-c": true, "/c": true, "/k": true, "-command": true, "-lc": true, "-ic": true}
)

func programName(tok string) string {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if i := strings.LastIndexAny(tok, `\/`); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

func isShellFlag(tok string) bool {
	if strings.HasPrefix(tok, "-") && len(tok) > 1 {
		return true
	}
	// cmd-style switches: /s /q /f /a:h. A longer slash word is a path.
	return strings.HasPrefix(tok, "/") && (len(tok) == 2 || (len(tok) > 2 && tok[2] == ':'))
}

func isRecursiveFlag(tok string) bool {
	l := strings.ToLower(tok)
	switch {
	case l == "/s", l == "--recursive":
		return true
	case strings.HasPrefix(l, "-rec"): // -Recurse and its abbreviations
		return true
	case len(l) <= 4 && l[0] == '-' && !strings.HasPrefix(l, "--"): // -r -R -rf -fr -Rf
		return strings.ContainsRune(l[1:], 'r')
	}
	return false
}

// shellDeletes returns every delete the command appears to perform.
func shellDeletes(cmd string, depth int) []shellDelete {
	if depth > 3 {
		return nil
	}
	toks := tokenizeShell(cmd)
	var out []shellDelete
	for i := 0; i < len(toks); {
		// A command starts at the beginning or after a separator.
		if toks[i].op {
			i++
			continue
		}
		j := i
		for j < len(toks) && !toks[j].op {
			j++
		}
		seg := toks[i:j]
		i = j
		for len(seg) > 0 && transparentPrefix[programName(seg[0].text)] {
			seg = seg[1:]
			for len(seg) > 0 && strings.HasPrefix(seg[0].text, "-") { // sudo -n, env -i ...
				seg = seg[1:]
			}
		}
		if len(seg) == 0 {
			continue
		}
		prog := programName(seg[0].text)
		switch {
		case shellWrappers[prog]:
			for k := 1; k < len(seg); k++ {
				if wrapperFlags[strings.ToLower(seg[k].text)] && k+1 < len(seg) {
					rest := make([]string, 0, len(seg)-k-1)
					for _, t := range seg[k+1:] {
						rest = append(rest, t.text)
					}
					out = append(out, shellDeletes(strings.Join(rest, " "), depth+1)...)
					break
				}
			}
		case deletePrograms[prog]:
			d := shellDelete{Program: prog}
			for _, t := range seg[1:] {
				if isShellFlag(t.text) {
					d.Recursive = d.Recursive || isRecursiveFlag(t.text)
				} else if t.text != "" {
					d.Operands = append(d.Operands, t.text)
				}
			}
			out = append(out, d)
		case prog == "find":
			hasDelete := false
			var roots []string
			for k, t := range seg[1:] {
				if strings.ToLower(t.text) == "-delete" {
					hasDelete = true
				}
				if k == len(roots) && !strings.HasPrefix(t.text, "-") && !strings.HasPrefix(t.text, "(") {
					roots = append(roots, t.text)
				}
			}
			if hasDelete {
				out = append(out, shellDelete{Program: "find -delete", Operands: roots, Recursive: true})
			}
		}
	}
	return out
}

var protectedDriveSubdirs = regexp.MustCompile(`^[a-z]:\\(windows(\\system32)?|program files( \(x86\))?|programdata|users(\\[^\\]+)?|documents and settings(\\[^\\]+)?)$`)

var protectedNames = map[string]bool{
	"~": true, "$home": true, "${home}": true, "%userprofile%": true, "$env:userprofile": true, "%homepath%": true, "$env:homepath": true,
	"%systemroot%": true, "$env:systemroot": true, "%windir%": true, "$env:windir": true, "%systemdrive%": true, "$env:systemdrive": true,
	"%programfiles%": true, "$env:programfiles": true, "%programdata%": true, "$env:programdata": true, "%homedrive%": true,
	"/": true, "/etc": true, "/usr": true, "/bin": true, "/sbin": true, "/lib": true, "/boot": true, "/var": true, "/home": true, "/root": true, "/opt": true, "/sys": true, "/proc": true, "/dev": true,
}

var unixHomeChild = regexp.MustCompile(`^/home/[^/]+$`)

// protectedTarget reports an operand that names a place nobody means to empty,
// and whether the operand used a wildcard.
func protectedTarget(operand string) (protected, wildcard bool) {
	orig := strings.ToLower(strings.TrimSpace(operand))
	wildcard = strings.ContainsAny(orig, "*?")
	// A wildcard or trailing separator under a place empties it just like the
	// place itself.
	p := orig
	for {
		q := strings.TrimSuffix(p, "*.*")
		q = strings.TrimSuffix(q, "*")
		q = strings.TrimSuffix(q, `\`)
		q = strings.TrimSuffix(q, "/")
		if q == p {
			break
		}
		p = q
	}
	if p == "" {
		// "/", "\" and "/*" are the filesystem root; a bare "*" is only the
		// current folder.
		return strings.HasPrefix(orig, "/") || strings.HasPrefix(orig, `\`), wildcard
	}
	if protectedNames[p] || unixHomeChild.MatchString(p) {
		return true, wildcard
	}
	win := strings.ReplaceAll(p, "/", `\`)
	for strings.Contains(win, `\\`) {
		win = strings.ReplaceAll(win, `\\`, `\`)
	}
	if len(win) == 2 && win[1] == ':' && win[0] >= 'a' && win[0] <= 'z' {
		return true, wildcard // C:
	}
	return protectedDriveSubdirs.MatchString(win), wildcard
}

// catastrophicDelete returns the first delete that would empty a protected place.
func catastrophicDelete(deletes []shellDelete) (shellDelete, bool) {
	for _, d := range deletes {
		for _, op := range d.Operands {
			if protected, wildcard := protectedTarget(op); protected && (d.Recursive || wildcard) {
				return d, true
			}
		}
	}
	return shellDelete{}, false
}
