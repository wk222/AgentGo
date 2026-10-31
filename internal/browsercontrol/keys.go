package browsercontrol

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type keySpec struct {
	key, code string
	vk        int
	text      string
	modifiers int // 1 Alt, 2 Control, 4 Meta, 8 Shift
	commands  []string
}

var namedKeys = map[string]keySpec{
	"enter":      {key: "Enter", code: "Enter", vk: 13, text: "\r"},
	"return":     {key: "Enter", code: "Enter", vk: 13, text: "\r"},
	"tab":        {key: "Tab", code: "Tab", vk: 9},
	"escape":     {key: "Escape", code: "Escape", vk: 27},
	"esc":        {key: "Escape", code: "Escape", vk: 27},
	"backspace":  {key: "Backspace", code: "Backspace", vk: 8},
	"delete":     {key: "Delete", code: "Delete", vk: 46},
	"space":      {key: " ", code: "Space", vk: 32, text: " "},
	"arrowup":    {key: "ArrowUp", code: "ArrowUp", vk: 38},
	"arrowdown":  {key: "ArrowDown", code: "ArrowDown", vk: 40},
	"arrowleft":  {key: "ArrowLeft", code: "ArrowLeft", vk: 37},
	"arrowright": {key: "ArrowRight", code: "ArrowRight", vk: 39},
	"up":         {key: "ArrowUp", code: "ArrowUp", vk: 38},
	"down":       {key: "ArrowDown", code: "ArrowDown", vk: 40},
	"left":       {key: "ArrowLeft", code: "ArrowLeft", vk: 37},
	"right":      {key: "ArrowRight", code: "ArrowRight", vk: 39},
	"home":       {key: "Home", code: "Home", vk: 36},
	"end":        {key: "End", code: "End", vk: 35},
	"pageup":     {key: "PageUp", code: "PageUp", vk: 33},
	"pagedown":   {key: "PageDown", code: "PageDown", vk: 34},
}

var modifierBits = map[string]int{"alt": 1, "control": 2, "ctrl": 2, "meta": 4, "cmd": 4, "shift": 8}

// editCommands lets a chord such as Control+A do its editing action: a key
// event sent over the protocol does not by itself trigger the browser's own
// shortcuts.
var editCommands = map[string]string{"a": "selectAll", "c": "copy", "x": "cut", "v": "paste", "z": "undo", "y": "redo"}

// parseKey reads "Enter", "a", "Shift+Tab" or "Control+A".
func parseKey(spec string) (keySpec, error) {
	parts := strings.Split(strings.TrimSpace(spec), "+")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" && len(parts) >= 2 && strings.TrimSpace(parts[len(parts)-2]) == "" {
		last = "+" // the plus key itself: "Shift++"
		parts = parts[:len(parts)-1]
	}
	if last == "" {
		return keySpec{}, fmt.Errorf("no key given")
	}
	mods := 0
	for _, m := range parts[:len(parts)-1] {
		bit, ok := modifierBits[strings.ToLower(strings.TrimSpace(m))]
		if !ok {
			return keySpec{}, fmt.Errorf("unknown modifier %q; use Control, Alt, Shift or Meta", m)
		}
		mods |= bit
	}
	if k, ok := namedKeys[strings.ToLower(last)]; ok {
		k.modifiers = mods
		if mods&(1|2|4) != 0 {
			k.text = ""
		}
		return k, nil
	}
	r := []rune(last)
	if len(r) != 1 {
		return keySpec{}, fmt.Errorf("unknown key %q; use a named key such as Enter, Tab, Escape, ArrowDown, PageDown, or a single character", last)
	}
	ch := r[0]
	k := keySpec{key: string(ch), modifiers: mods}
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
		up := unicode.ToUpper(ch)
		k.code, k.vk = "Key"+string(up), int(up)
	case ch >= '0' && ch <= '9':
		k.code, k.vk = "Digit"+string(ch), int(ch)
	}
	if mods&(1|2|4) == 0 {
		k.text = string(ch)
	} else if cmd, ok := editCommands[strings.ToLower(string(ch))]; ok && mods&2 != 0 {
		k.commands = []string{cmd}
	}
	return k, nil
}

func (c *Controller) sendKey(ctx context.Context, t *tab, spec string) error {
	k, err := parseKey(spec)
	if err != nil {
		return err
	}
	conn := c.connOrNil()
	if conn == nil {
		return errConnClosed
	}
	down := map[string]any{"type": "rawKeyDown", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": k.modifiers}
	if k.text != "" {
		down["type"], down["text"], down["unmodifiedText"] = "keyDown", k.text, k.text
	}
	if len(k.commands) > 0 {
		down["commands"] = k.commands
	}
	if err := conn.call(ctx, t.session, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	up := map[string]any{"type": "keyUp", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": k.modifiers}
	return conn.call(ctx, t.session, "Input.dispatchKeyEvent", up, nil)
}
