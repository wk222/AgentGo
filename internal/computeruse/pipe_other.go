//go:build !windows

package computeruse

import (
	"context"
	"net"
)

func userKey() string { return "user" }

// PipeName has no meaning off Windows.
func PipeName() string { return "" }

// Listen is Windows-only (go-winio named pipes).
func Listen(string) (net.Listener, error) { return nil, ErrUnsupported }

// DialPipe is Windows-only (go-winio named pipes).
func DialPipe(context.Context, string) (net.Conn, error) { return nil, ErrUnsupported }
