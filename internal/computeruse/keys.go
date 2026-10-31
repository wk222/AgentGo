package computeruse

import (
	"fmt"
	"strings"
)

// Key-combo parsing, adapted from octo-agent's internal/computer (MIT). It is
// platform-neutral so it can be tested anywhere; substrate_windows.go maps the
// canonical names to virtual-key codes.

const (
	flagShift uint64 = 1 << iota
	flagControl
	flagAlt
	flagWin
)

var keyAliases = map[string]string{
	"return": "enter", "enter": "enter",
	"tab": "tab", "space": "space",
	"backspace": "backspace",
	"delete":    "delete", "del": "delete", "forwarddelete": "delete",
	"escape": "escape", "esc": "escape",
	"left": "left", "right": "right", "down": "down", "up": "up",
	"home": "home", "end": "end", "pageup": "pageup", "pagedown": "pagedown",
	"f1": "f1", "f2": "f2", "f3": "f3", "f4": "f4", "f5": "f5", "f6": "f6",
	"f7": "f7", "f8": "f8", "f9": "f9", "f10": "f10", "f11": "f11", "f12": "f12",
}

var modifierNames = map[string]uint64{
	"shift": flagShift, "ctrl": flagControl, "control": flagControl,
	"alt": flagAlt, "option": flagAlt, "opt": flagAlt,
	"win": flagWin, "cmd": flagWin, "command": flagWin, "meta": flagWin, "super": flagWin,
}

// parseCombo splits "ctrl+shift+enter" into a canonical key and modifier
// flags. Exactly one non-modifier key is required: a modifier-only combo must
// fail rather than press nothing.
func parseCombo(combo string) (key string, flags uint64, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+")
	keys := 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return "", 0, fmt.Errorf("malformed key combo %q", combo)
		}
		if f, ok := modifierNames[p]; ok {
			flags |= f
			continue
		}
		if name, ok := keyAliases[p]; ok {
			key = name
			keys++
			continue
		}
		if len(p) == 1 && p[0] > ' ' && p[0] <= '~' {
			key = p
			keys++
			continue
		}
		return "", 0, fmt.Errorf("unknown key %q in combo %q", p, combo)
	}
	switch {
	case keys == 0:
		return "", 0, fmt.Errorf("key combo %q has no key, only modifiers", combo)
	case keys > 1:
		return "", 0, fmt.Errorf("key combo %q names more than one key (use type for text)", combo)
	}
	return key, flags, nil
}

// isDestructiveCombo reports chords that delete, cut, close or lock. A batch
// runs without a look in between, so these need explicit confirmation.
func isDestructiveCombo(combo string) bool {
	key, flags, err := parseCombo(combo)
	if err != nil {
		return false
	}
	ctrl, alt, win := flags&flagControl != 0, flags&flagAlt != 0, flags&flagWin != 0
	switch {
	case key == "delete":
		return true
	case alt && key == "f4", ctrl && key == "f4":
		return true
	case ctrl && (key == "w" || key == "q" || key == "x"):
		return true
	case win && key == "l":
		return true
	}
	return false
}
