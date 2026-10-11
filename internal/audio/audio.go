// Package audio defines the kernel side of real-time voice: frames, events and the
// cancellation contract. It deliberately contains NO signal processing: VAD, ASR
// and TTS are plugins (docs/kernel-plugin-consensus.md §3). What must live in the
// kernel is the one thing plugins cannot coordinate among themselves - who stops
// when the user starts talking.
//
// The barge-in sequence:
//
//	mic frames -> ASR plugin (partial text) -> kernel sees speech start while TTS
//	is playing -> AudioControl.Interrupt -> engine.Router.Interrupt: the run's
//	context is cancelled and ONE engine.EventInterrupted is emitted -> the TTS
//	plugin stops because its context is cancelled and the channel stops rendering
//	-> the final ASR transcript becomes the next run's input.
//
// This package must stay stdlib only and must not import internal/bridge.
package audio

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Format describes PCM audio. Plugins that need other codecs convert at their edge.
type Format struct {
	SampleRate int `json:"sample_rate"` // Hz, e.g. 16000
	Channels   int `json:"channels"`    // 1 = mono
	BitDepth   int `json:"bit_depth"`   // 16 = signed 16-bit little endian
}

// Frame is one slice of audio. Large audio travels as frames or by reference;
// it is never embedded in an engine.ContentBlock.
type Frame struct {
	Format Format        `json:"format"`
	Data   []byte        `json:"-"`
	At     time.Duration `json:"at"` // offset from the session start
	Final  bool          `json:"final,omitempty"`
}

// EventKind classifies an AudioEvent.
type EventKind string

const (
	EventSpeechStart EventKind = "speech_start" // the user started talking (VAD)
	EventSpeechEnd   EventKind = "speech_end"   // the user stopped talking
	EventTranscript  EventKind = "transcript"   // recognised text; Partial until final
	EventPlayback    EventKind = "playback"     // TTS started/stopped playing (Text: "start"|"stop")
	EventInterrupted EventKind = "interrupted"  // the session was interrupted
)

// Event is what the kernel observes on an audio session.
type Event struct {
	Kind    EventKind `json:"kind"`
	Text    string    `json:"text,omitempty"`
	Partial bool      `json:"partial,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

// Control is the kernel's handle on a running session.
type Control interface {
	// Interrupt stops whatever is speaking or generating for this session. It is
	// idempotent and safe from any goroutine. The reason is carried into the
	// engine's EventInterrupted ("barge-in", "user", ...).
	Interrupt(reason string) error
	// Close ends the session and releases its resources.
	Close() error
}

// Session starts a duplex audio session bound to a chat session id.
type Session interface {
	Start(ctx context.Context, sessionID string) (<-chan Event, Control, error)
}

// ErrClosed is returned by a Control after Close.
var ErrClosed = errors.New("audio: session closed")

// Interrupter is what a Control calls to stop the active run. engine.Router
// provides it (Router.Interrupt); it is a func here so this package does not
// import engine.
type Interrupter func(sessionID, reason string) bool

// NewControl builds the standard Control: Interrupt forwards to the Interrupter
// for sessionID and publishes an EventInterrupted to the session's event sink.
// onClose, if set, runs once on Close.
func NewControl(sessionID string, interrupt Interrupter, publish func(Event), onClose func()) Control {
	return &control{sessionID: sessionID, interrupt: interrupt, publish: publish, onClose: onClose}
}

type control struct {
	sessionID string
	interrupt Interrupter
	publish   func(Event)
	onClose   func()

	mu     sync.Mutex
	closed bool
}

func (c *control) Interrupt(reason string) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if reason == "" {
		reason = "user"
	}
	if c.interrupt != nil {
		c.interrupt(c.sessionID, reason)
	}
	if c.publish != nil {
		c.publish(Event{Kind: EventInterrupted, Reason: reason, At: time.Now()})
	}
	return nil
}

func (c *control) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}
