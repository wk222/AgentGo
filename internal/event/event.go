package event

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ResponseError wraps error details for the LLM response.
type ResponseError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Choice represents a segment of model choices.
type Choice struct {
	Index        int    `json:"index"`
	Message      string `json:"message,omitempty"` // Message content or tool call payload
	Delta        string `json:"delta,omitempty"`   // Streaming chunk
	FinishReason string `json:"finish_reason,omitempty"`
}

// Response models the main payload of LLM outputs.
type Response struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object,omitempty"`
	Created int64          `json:"created,omitempty"`
	Done    bool           `json:"done"`
	Choices []Choice       `json:"choices,omitempty"`
	Error   *ResponseError `json:"error,omitempty"`
}

// Event represents an individual message/state frame emitted by the agent runner.
type Event struct {
	Response *Response `json:"response,omitempty"`

	RequestID          string    `json:"requestID,omitempty"`
	InvocationID       string    `json:"invocationId"`
	ParentInvocationID string    `json:"parentInvocationId,omitempty"`
	Author             string    `json:"author"`
	ID                 string    `json:"id"`
	Timestamp          time.Time `json:"timestamp"`
	Branch             string    `json:"branch,omitempty"`
	Tag                string    `json:"tag,omitempty"` // Semantic tag (e.g. code_execution_code)
	RequiresCompletion bool      `json:"requiresCompletion,omitempty"`
}

// Option configures an Event.
type Option func(*Event)

// WithResponse sets the response of the event.
func WithResponse(r *Response) Option {
	return func(e *Event) {
		e.Response = r
	}
}

// WithTag sets the tag annotation of the event.
func WithTag(t string) Option {
	return func(e *Event) {
		e.Tag = t
	}
}

// WithBranch sets the execution branch of the event.
func WithBranch(b string) Option {
	return func(e *Event) {
		e.Branch = b
	}
}

// WithRequestID sets the request ID.
func WithRequestID(id string) Option {
	return func(e *Event) {
		e.RequestID = id
	}
}

// WithParentInvocationID sets the parent invocation ID.
func WithParentInvocationID(id string) Option {
	return func(e *Event) {
		e.ParentInvocationID = id
	}
}

// WithRequiresCompletion sets the completion notification flag.
func WithRequiresCompletion(rc bool) Option {
	return func(e *Event) {
		e.RequiresCompletion = rc
	}
}

// New creates a new Event frame.
func New(invocationID, author string, opts ...Option) *Event {
	e := &Event{
		Response:     &Response{},
		ID:           uuid.NewString(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// EmitEvent sends an event to the channel, blocking if the channel is full,
// but respecting context cancellation.
func EmitEvent(ctx context.Context, ch chan<- *Event, e *Event) error {
	select {
	case ch <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EmitEventWithTimeout sends an event to the channel with a timeout threshold.
func EmitEventWithTimeout(ctx context.Context, ch chan<- *Event, e *Event, timeout time.Duration) error {
	if e == nil || ch == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if timeout <= 0 {
		select {
		case ch <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ch <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("emit event timeout")
	}
}
