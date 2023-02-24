package streaming

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

// No value.
type Void *struct{}

// The type of a dummy value, ().
type Unit struct{}

// Dummy value, ().
var unitValue = Unit(struct{}{})

// Either nil or a reference to T.
type Maybe[T any] *T

// Applies a function to the value in a Maybe.
func MapMaybe[A, B any](maybeA Maybe[A], f func(A) B) Maybe[B] {
	if maybeA == nil {
		return nil
	} else {
		b := f(*maybeA)
		return Maybe[B](&b)
	}
}

// Either T or error.
type Result[T any] struct {
	Value *T
	Error *error
}

// Result with a T.
func Value[T any](value T) Result[T] {
	return Result[T]{Value: &value}
}

// Result with an error.
func Error[T any](err error) Result[T] {
	return Result[T]{Error: &err}
}

// Coalesces an error with a default.
func ErrorDefault[T any](err error, defalt T) Result[T] {
	if err == nil {
		return Result[T]{Value: &defalt}
	} else {
		return Result[T]{Error: &err}
	}
}

// Context that a stream needs in order to run, e.g. how to receive/send
// values upstream/downstream.
type Env[In, Out any] struct {
	// For cancellation.
	Ctx context.Context

	// Receive a value from upstream.
	Recv func() Maybe[In]

	// Send a value downstream. Returns true if downstream is requesting another value.
	Send func(Out) bool
}

// A stream of In values coming in and Out values going out.
type Stream[In, Out, Return any] func(Env[In, Out]) Result[Return]

// Connects 2 streams together.
func Connect[A, B, C, R1, R2 any](a2b Stream[A, B, R1], b2c Stream[B, C, R2]) Stream[A, C, R2] {
	return Stream[A, C, R2](func(env Env[A, C]) Result[R2] {
		maybeBs := make(chan Maybe[B])
		requests := make(chan Unit)
		end := make(chan Unit, 1)
		defer close(end)

		a2bSend := func(b B) bool {
			maybeBs <- Maybe[B](&b)
			select {
			case <-requests:
				return true
			case <-end:
				return false
			}
		}

		b2cRecv := func() Maybe[B] {
			requests <- unitValue
			return <-maybeBs
		}

		ctx, cancel := context.WithCancel(env.Ctx)

		r2 := make(chan Result[R2], 1)

		go func() {
			select {
			case <-requests:
				r1 := a2b(Env[A, B]{Ctx: env.Ctx, Recv: env.Recv, Send: a2bSend})
				if r1.Error != nil {
					cancel()
					r2 <- Error[R2](*r1.Error)
				}
				maybeBs <- Maybe[B](nil)
			case <-end:
				return
			}
		}()

		go func() {
			r2 <- b2c(Env[B, C]{Ctx: ctx, Recv: b2cRecv, Send: env.Send})
		}()

		return <-r2
	})
}

// Runs a stream.
func Run[R any](ctx context.Context, stream Stream[Void, Void, R]) Result[R] {
	return stream(Env[Void, Void]{
		Ctx:  ctx,
		Recv: func() Maybe[Void] { return Maybe[Void](nil) },
		Send: func(_ Void) bool { return false },
	})
}

// Makes a stream from a []T.
func SourceSlice[T any](ts []T) Stream[Void, T, Unit] {
	return Stream[Void, T, Unit](func(env Env[Void, T]) Result[Unit] {
		for _, t := range ts {
			if !env.Send(t) {
				break
			}
		}
		return Value(unitValue)
	})
}

// Makes a stream from a []T.
func SinkSlice[T any]() Stream[T, Void, []T] {
	return Stream[T, Void, []T](func(env Env[T, Void]) Result[[]T] {
		ts := []T{}
		for maybeT := env.Recv(); maybeT != nil; maybeT = env.Recv() {
			ts = append(ts, *maybeT)
		}
		return Value(ts)
	})
}

// Maps the value in a Result[T].
func MapValue[A, B any](f func(a A) B, result Result[A]) Result[B] {
	if result.Error != nil {
		return Error[B](*result.Error)
	}
	return Value(f(*result.Value))
}

// Maps the return value of a stream.
func MapReturn[A, B, R1, R2 any](stream Stream[A, B, R1], f func(r1 R1) R2) Stream[A, B, R2] {
	return Stream[A, B, R2](func(env Env[A, B]) Result[R2] {

		return MapValue(f, stream(env))
	})
}

// Makes a stream that returns all input concatenated.
func SinkString() Stream[string, Void, string] {
	return MapReturn(
		SinkSlice[string](),
		func(s []string) string { return strings.Join(s, "") },
	)
}

// Makes a stream that returns the given value.
func SinkNull[T, R any](r R) Stream[T, Void, R] {
	return Stream[T, Void, R](func(env Env[T, Void]) Result[R] {
		for env.Recv() != nil {
		}
		return Value(r)
	})
}

// Takes n values from a stream.
func Take[T any](n int) Stream[T, T, Unit] {
	return Stream[T, T, Unit](func(env Env[T, T]) Result[Unit] {
		for i := 0; i < n; i++ {
			v := env.Recv()
			if v == nil {
				break
			}
			if !env.Send(*v) {
				break
			}
		}
		return Value(unitValue)
	})
}

// Drops n values from a stream.
func Drop[T any](n int) Stream[T, T, Unit] {
	return Stream[T, T, Unit](func(env Env[T, T]) Result[Unit] {
		for i := 0; i < n; i++ {
			v := env.Recv()
			if v == nil {
				return Value(unitValue)
			}
		}

		for {
			v := env.Recv()
			if v == nil {
				return Value(unitValue)
			}
			if !env.Send(*v) {
				return Value(unitValue)
			}
		}
	})
}

// Streams stdout from a command.
func SourceExec(createCmd func(context.Context) *exec.Cmd, stop func(*exec.Cmd) error) Stream[Void, []byte, Unit] {
	return Stream[Void, []byte, Unit](func(env Env[Void, []byte]) Result[Unit] {
		cmd := createCmd(env.Ctx)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return Error[Unit](err)
		}

		err = cmd.Start()
		if err != nil {
			return Error[Unit](err)
		}

		res := read(stdout, env.Send)

		stopErr := stop(cmd)

		return coalesceResults(res, ErrorDefault(stopErr, unitValue))
	})
}

func coalesceResults[T any](rs ...Result[T]) Result[T] {
	for _, r := range rs {
		if r.Error != nil {
			return r
		}
	}
	for _, r := range rs {
		return r
	}
	var r Result[T]
	return r
}

// Streams stdin to a command.
func SinkExec(createCmd func(context.Context) *exec.Cmd, stop func(*exec.Cmd) error) Stream[[]byte, Void, Unit] {
	return Stream[[]byte, Void, Unit](func(env Env[[]byte, Void]) (r Result[Unit]) {
		cmd := createCmd(env.Ctx)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return Error[Unit](err)
		}

		err = cmd.Start()
		if err != nil {
			return Error[Unit](err)
		}

		defer func() {
			err := stop(cmd)
			if r.Error == nil && err != nil {
				r = Error[Unit](err)
			}
		}()

		return write(stdin, env.Recv)
	})
}

// Streams bytes from an io.Reader.
func SourceReader(reader io.Reader) Stream[Void, []byte, Unit] {
	return Stream[Void, []byte, Unit](func(env Env[Void, []byte]) (r Result[Unit]) {
		return read(reader, env.Send)
	})
}

// Streams bytes into an io.Writer.
func SinkWriter(writer io.Writer) Stream[[]byte, Void, Unit] {
	return Stream[[]byte, Void, Unit](func(env Env[[]byte, Void]) (r Result[Unit]) {
		return write(writer, env.Recv)
	})
}

func read(reader io.Reader, cb func([]byte) bool) Result[Unit] {
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err != nil && err != io.EOF {
			return Error[Unit](err)
		}

		if n > 0 {
			if !cb(buf[:n]) {
				return Value(unitValue)
			}
		}

		if err == io.EOF {
			return Value(unitValue)
		}
	}
}

func write(writer io.Writer, more func() Maybe[[]byte]) Result[Unit] {
	for maybeBs := more(); maybeBs != nil; maybeBs = more() {
		_, err := writer.Write(*maybeBs)
		if err != nil {
			return Error[Unit](err)
		}
	}

	return Value(unitValue)
}

func coalesceErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Makes a stream from a pure function.
func FuncToStreamPure[A, B any](f func(a A) B) Stream[A, B, Unit] {
	return FuncToStreamCtx(func(ctx context.Context, a A) Result[B] {
		return Value(f(a))
	})
}

// Makes a stream from a function.
func FuncToStream[A, B any](f func(a A) Result[B]) Stream[A, B, Unit] {
	return FuncToStreamCtx(func(ctx context.Context, a A) Result[B] {
		return f(a)
	})
}

// Makes a stream from a function and passes the context.
func FuncToStreamCtx[A, B any](f func(ctx context.Context, a A) Result[B]) Stream[A, B, Unit] {
	return Stream[A, B, Unit](func(env Env[A, B]) Result[Unit] {
		for maybeA := env.Recv(); maybeA != nil; maybeA = env.Recv() {
			b := f(env.Ctx, *maybeA)
			if b.Error != nil {
				return Error[Unit](*b.Error)
			}
			if !env.Send(*b.Value) {
				break
			}
		}
		return Value(unitValue)
	})
}

// Maps the values coming into a stream.
func MapIn[A, B, C, R any](stream Stream[B, C, R], f func(a A) B) Stream[A, C, R] {
	return Stream[A, C, R](func(env Env[A, C]) Result[R] {
		return stream(Env[B, C]{
			Ctx:  env.Ctx,
			Recv: func() Maybe[B] { return MapMaybe(env.Recv(), f) },
			Send: env.Send,
		})
	})
}

// Maps the values going out of a stream.
func MapOut[A, B, C, R any](stream Stream[A, B, R], f func(b B) C) Stream[A, C, R] {
	return Stream[A, C, R](func(env Env[A, C]) Result[R] {
		return stream(Env[A, B]{
			Ctx:  env.Ctx,
			Recv: env.Recv,
			Send: func(b B) bool { return env.Send(f(b)) },
		})
	})
}

// Splits by a predicate.
func SplitBy[T any](predicate func(t T) bool) Stream[T, Stream[Void, T, Unit], Unit] {
	return Stream[T, Stream[Void, T, Unit], Unit](func(env Env[T, Stream[Void, T, Unit]]) Result[Unit] {
		bail := false
		inner := Stream[Void, T, Unit](
			func(env2 Env[Void, T]) Result[Unit] {
				for {
					v := env.Recv()
					if v == nil {
						bail = true
						return Value(unitValue)
					}
					if predicate(*v) {
						return Value(unitValue)
					}
					if !env2.Send(*v) {
						return Value(unitValue)
					}
				}
			})

		for env.Send(inner) {
			if bail {
				break
			}
		}

		return Value(unitValue)
	})
}

// Splits on a value.
func SplitOn[T comparable](delimiter T) Stream[T, Stream[Void, T, Unit], Unit] {
	return SplitBy(func(t T) bool { return t == delimiter })
}

// Folds values in a stream.
func Fold[V any, R any](acc R, fold func(acc R, v V) R) Stream[V, Void, R] {
	return Stream[V, Void, R](func(env Env[V, Void]) Result[R] {
		for v := env.Recv(); v != nil; v = env.Recv() {
			acc = fold(acc, *v)
		}
		return Value(acc)
	})
}

// Sums the values in a stream.
func Sum() Stream[int, Void, int] {
	return Fold(0, func(acc int, v int) int { return acc + v })
}

// Finds the minimum value. Returns nil if there were no values.
func Min() Stream[int, Void, *int] {
	return Fold(nil, func(acc *int, v int) *int {
		if acc == nil {
			return &v
		}

		if *acc < v {
			return acc
		} else {
			return &v
		}
	})
}

// Finds the maximum value. Returns nil if there were no values.
func Max() Stream[int, Void, *int] {
	return Fold(nil, func(acc *int, v int) *int {
		if acc == nil {
			return &v
		}

		if *acc < v {
			return &v
		} else {
			return acc
		}
	})
}

// Counts the values in a stream.
func Count[T any]() Stream[T, Void, int] {
	return Fold(0, func(acc int, _ T) int { return acc + 1 })
}

// Flattens a stream of slices by emitting each element individually.
func Concat[T any]() Stream[[]T, T, Unit] {
	return Stream[[]T, T, Unit](func(env Env[[]T, T]) Result[Unit] {
		for ts := env.Recv(); ts != nil; ts = env.Recv() {
			for _, t := range *ts {
				if !env.Send(t) {
					return Value(unitValue)
				}
			}
		}

		return Value(unitValue)
	})
}
