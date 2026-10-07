package lifecycle

import (
	"errors"
)

// Disposer is a clean-up callback that completely rolls back a side-effect.
type Disposer func() error

// NoopDisposer is a convenience empty disposer.
func NoopDisposer() error {
	return nil
}

// CombineDisposers aggregates multiple disposers into a single LIFO-executing disposer.
func CombineDisposers(disposers ...Disposer) Disposer {
	return func() error {
		var errs []error
		// Run in reverse registration order (LIFO)
		for i := len(disposers) - 1; i >= 0; i-- {
			d := disposers[i]
			if d != nil {
				if err := d(); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}
}

// EffectFunc is a factory function that acquires a resource and returns its cleanup disposer.
type EffectFunc func() (Disposer, error)
