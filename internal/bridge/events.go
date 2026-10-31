package bridge

import "sync"

// EventSink receives every UI event the backend emits ("chat:chunk",
// "terminal:data", ...). The desktop build forwards events to the Wails window;
// the web shell registers a sink that streams them to browser tabs.
type EventSink func(name string, data any)

var eventSinks struct {
	sync.RWMutex
	list []EventSink
}

// AddEventSink registers a sink for the lifetime of the process.
func AddEventSink(fn EventSink) {
	if fn == nil {
		return
	}
	eventSinks.Lock()
	eventSinks.list = append(eventSinks.list, fn)
	eventSinks.Unlock()
}

func hasEventSink() bool {
	eventSinks.RLock()
	defer eventSinks.RUnlock()
	return len(eventSinks.list) > 0
}

// eventsOn reports whether anything is listening for UI events.
func eventsOn() bool { return hasEventSink() || desktopEventsReady() }

// emitEvent delivers one UI event to the web sinks and to the desktop window.
func emitEvent(name string, data any) {
	eventSinks.RLock()
	sinks := eventSinks.list
	eventSinks.RUnlock()
	for _, fn := range sinks {
		fn(name, data)
	}
	desktopEmit(name, data)
}

func (s *AppService) eventsOn() bool { return eventsOn() }

func (s *AppService) emit(name string, data any) { emitEvent(name, data) }

// StartWebEvents wires service-level event sources that the Wails runtime would
// normally start through ServiceStartup. Call once before serving a browser UI.
func (s *AppService) StartWebEvents() { s.startTaskEvents() }
