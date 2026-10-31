package webshell

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

// dispatcher calls exported methods of a service by name, the way the Wails
// runtime does for Call.ByName: JSON arguments in, JSON result (or error) out.
type dispatcher struct {
	methods map[string]reflect.Value
}

func newDispatcher(service any) *dispatcher {
	v := reflect.ValueOf(service)
	t := v.Type()
	d := &dispatcher{methods: make(map[string]reflect.Value, t.NumMethod())}
	for i := 0; i < t.NumMethod(); i++ {
		name := t.Method(i).Name
		// Lifecycle hooks (ServiceStartup, ServiceShutdown, ...) are not callable.
		if strings.HasPrefix(name, "Service") {
			continue
		}
		d.methods[name] = v.Method(i)
	}
	return d
}

// has reports whether the service exposes method.
func (d *dispatcher) has(method string) bool {
	_, ok := d.methods[method]
	return ok
}

// call runs method with the JSON-encoded args. A panic in the service becomes
// an error so one bad call cannot take the shell down.
func (d *dispatcher) call(ctx context.Context, method string, args []json.RawMessage) (result any, err error) {
	m, ok := d.methods[method]
	if !ok {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("%s panicked: %v", method, r)
		}
	}()

	mt := m.Type()
	in := make([]reflect.Value, 0, mt.NumIn())
	next := 0 // next JSON argument to consume
	for i := 0; i < mt.NumIn(); i++ {
		pt := mt.In(i)
		switch {
		case pt == ctxType:
			in = append(in, reflect.ValueOf(ctx))
		case mt.IsVariadic() && i == mt.NumIn()-1:
			rest := reflect.MakeSlice(pt, 0, len(args)-next)
			for ; next < len(args); next++ {
				ev := reflect.New(pt.Elem())
				if err := json.Unmarshal(args[next], ev.Interface()); err != nil {
					return nil, fmt.Errorf("%s: argument %d: %w", method, next+1, err)
				}
				rest = reflect.Append(rest, ev.Elem())
			}
			in = append(in, rest)
		default:
			pv := reflect.New(pt)
			if next < len(args) {
				if err := json.Unmarshal(args[next], pv.Interface()); err != nil {
					return nil, fmt.Errorf("%s: argument %d: %w", method, next+1, err)
				}
				next++
			}
			in = append(in, pv.Elem())
		}
	}
	if next < len(args) {
		return nil, fmt.Errorf("%s: expected %d arguments, got %d", method, next, len(args))
	}

	var out []reflect.Value
	if mt.IsVariadic() {
		out = m.CallSlice(in)
	} else {
		out = m.Call(in)
	}
	for _, o := range out {
		if o.Type() == errType {
			if !o.IsNil() {
				return nil, o.Interface().(error)
			}
			continue
		}
		if result == nil {
			result = o.Interface()
		}
	}
	return result, nil
}
