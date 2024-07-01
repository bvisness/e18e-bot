package utils

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"time"
)

func And[T comparable](a, b T) T {
	var zero T
	if a != zero {
		return b
	}
	return a
}

func Or[T comparable](a, b T) T {
	var zero T
	if a != zero {
		return a
	}
	return b
}

func Assert[T comparable](v T, format string, args ...any) T {
	var zero T
	if v == zero {
		panic(fmt.Errorf("failed assert: "+format, args...))
	}
	return v
}

// Takes an (error) return and panics if there is an error.
// Helps avoid `if err != nil` in scripts. Use sparingly in real code.
func Must(err error) {
	if err != nil {
		panic(err)
	}
}

// Takes a (something, error) return and panics if there is an error.
// Helps avoid `if err != nil` in scripts. Use sparingly in real code.
func Must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

/*
Recover a panic and convert it to a returned error. Call it like so:

	func MyFunc() (err error) {
		defer utils.RecoverPanicAsError(&err)
	}

If an error was already present, the panicked error will take precedence. Unfortunately there's
no good way to include both errors because you can't really have two chains of errors and still
play nice with the standard library's Unwrap behavior. But most of the time this shouldn't be an
issue, since the panic will probably occur before a meaningful error value was set.
*/
func RecoverPanicAsError(err *error) {
	if r := recover(); r != nil {
		var recoveredErr error
		if rerr, ok := r.(error); ok {
			recoveredErr = rerr
		} else {
			recoveredErr = fmt.Errorf("panic with value: %v", r)
		}
		*err = fmt.Errorf("panic recovered as error: %w", recoveredErr)
	}
}

func RecoverPanicAsErrorAndLog(err *error, logger *slog.Logger) {
	if r := recover(); r != nil {
		Or(logger, slog.Default()).Error("panic being recovered as error", "recovered", r)
		os.Stderr.Write(debug.Stack())

		var recoveredErr error
		if rerr, ok := r.(error); ok {
			recoveredErr = rerr
		} else {
			recoveredErr = fmt.Errorf("panic with value: %v", r)
		}
		*err = fmt.Errorf("panic recovered as error: %w", recoveredErr)
	}
}

var ErrSleepInterrupted = errors.New("sleep interrupted by context cancellation")

func SleepContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ErrSleepInterrupted
	case <-time.After(d):
		return nil
	}
}

type PointerLogValue[T any] struct {
	value *T
}

var _ slog.LogValuer = &PointerLogValue[any]{}

func LogPtr[T any](v *T) PointerLogValue[T] {
	return PointerLogValue[T]{v}
}

func (pv PointerLogValue[T]) LogValue() slog.Value {
	if pv.value == nil {
		return slog.AnyValue(pv.value)
	}
	return slog.AnyValue(*pv.value)
}
