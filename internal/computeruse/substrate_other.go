//go:build !windows

package computeruse

import "image"

// Supported reports whether this build has a desktop substrate.
func Supported() bool { return false }

// NewSubstrate returns a substrate whose every call fails with ErrUnsupported,
// so the package compiles everywhere and the tool degrades to a clear error.
func NewSubstrate() Substrate { return stubSubstrate{} }

type stubSubstrate struct{}

func (stubSubstrate) ScreenSize() (int, int, error) { return 0, 0, ErrUnsupported }
func (stubSubstrate) Capture() (*image.RGBA, error) { return nil, ErrUnsupported }
func (stubSubstrate) Preflight(bool, float64, float64) *Error {
	return NewError(CodeUnsupported, "%v", ErrUnsupported)
}
func (stubSubstrate) MoveTo(x, y float64) error                 { return ErrUnsupported }
func (stubSubstrate) Click(string, float64, float64, int) error { return ErrUnsupported }
func (stubSubstrate) Scroll(dx, dy float64) error               { return ErrUnsupported }
func (stubSubstrate) Drag([]Point) error                        { return ErrUnsupported }
func (stubSubstrate) TypeText(string) error                     { return ErrUnsupported }
func (stubSubstrate) Press(string) error                        { return ErrUnsupported }
