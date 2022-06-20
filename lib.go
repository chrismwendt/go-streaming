package streaming

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

type Void *struct{}

type Unit struct{}

var unit = Unit(struct{}{})

type Maybe[T any] *T

func mapMaybe[A, B any](maybeA Maybe[A], f func(A) B) Maybe[B] {
	if maybeA == nil {
		return nil
	} else {
		b := f(*maybeA)
		return Maybe[B](&b)
	}
}

type Result[T any] struct {
	Value *T
	Error *error
}

func Value[T any](value T) Result[T] {
	return Result[T]{Value: &value}
}

func Error[T any](err error) Result[T] {
	return Result[T]{Error: &err}
}

func ErrorDefault[T any](err error, defalt T) Result[T] {
	if err == nil {
		return Result[T]{Value: &defalt}
	} else {
		return Result[T]{Error: &err}
	}
}

type Env[In, Out any] struct {
	ctx  context.Context
	recv func() Maybe[In]
	send func(Out) bool
}

type Stream[In, Out, Return any] func(Env[In, Out]) Result[Return]

func connect[A, B, C, R1, R2 any](a2b Stream[A, B, R1], b2c Stream[B, C, R2]) Stream[A, C, R2] {
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
			requests <- unit
			return <-maybeBs
		}

		ctx, cancel := context.WithCancel(env.ctx)

		r2 := make(chan Result[R2], 1)

		go func() {
			select {
			case <-requests:
				r1 := a2b(Env[A, B]{ctx: env.ctx, recv: env.recv, send: a2bSend})
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
			r2 <- b2c(Env[B, C]{ctx: ctx, recv: b2cRecv, send: env.send})
		}()

		return <-r2
	})
}

func run[R any](ctx context.Context, stream Stream[Void, Void, R]) Result[R] {
	return stream(Env[Void, Void]{
		ctx:  ctx,
		recv: func() Maybe[Void] { return Maybe[Void](nil) },
		send: func(_ Void) bool { return false },
	})
}

func sourceSlice[T any](ts []T) Stream[Void, T, Unit] {
	return Stream[Void, T, Unit](func(env Env[Void, T]) Result[Unit] {
		for _, t := range ts {
			if !env.send(t) {
				break
			}
		}
		return Value(unit)
	})
}

func sinkSlice[T any]() Stream[T, Void, []T] {
	return Stream[T, Void, []T](func(env Env[T, Void]) Result[[]T] {
		ts := []T{}
		for maybeT := env.recv(); maybeT != nil; maybeT = env.recv() {
			ts = append(ts, *maybeT)
		}
		return Value(ts)
	})
}

func mapValue[A, B any](f func(a A) B, result Result[A]) Result[B] {
	if result.Error != nil {
		return Error[B](*result.Error)
	}
	return Value(f(*result.Value))
}

func mapReturn[A, B, R1, R2 any](stream Stream[A, B, R1], f func(r1 R1) R2) Stream[A, B, R2] {
	return Stream[A, B, R2](func(env Env[A, B]) Result[R2] {

		return mapValue(f, stream(env))
	})
}

func sinkString() Stream[string, Void, string] {
	return mapReturn(
		sinkSlice[string](),
		func(s []string) string { return strings.Join(s, "") },
	)
}

func sinkNull[T, R any](r R) Stream[T, Void, R] {
	return Stream[T, Void, R](func(env Env[T, Void]) Result[R] {
		for env.recv() != nil {
		}
		return Value(r)
	})
}

func take[T any](n int) Stream[T, T, Unit] {
	return Stream[T, T, Unit](func(env Env[T, T]) Result[Unit] {
		for i := 0; i < n; i++ {
			v := env.recv()
			if v == nil {
				break
			}
			if !env.send(*v) {
				break
			}
		}
		return Value(unit)
	})
}

func sourceExec(createCmd func(context.Context) *exec.Cmd, stop func(*exec.Cmd) error) Stream[Void, []byte, Unit] {
	return Stream[Void, []byte, Unit](func(env Env[Void, []byte]) Result[Unit] {
		cmd := createCmd(env.ctx)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return Error[Unit](err)
		}

		err = cmd.Start()
		if err != nil {
			return Error[Unit](err)
		}

		res := read(stdout, env.send)

		stopErr := stop(cmd)

		return coalesce(res, ErrorDefault(stopErr, unit))
	})
}

func coalesce[T any](rs ...Result[T]) Result[T] {
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

func sinkExec(createCmd func(context.Context) *exec.Cmd, stop func(*exec.Cmd) error) Stream[[]byte, Void, Unit] {
	return Stream[[]byte, Void, Unit](func(env Env[[]byte, Void]) (r Result[Unit]) {
		cmd := createCmd(env.ctx)

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

		return write(stdin, env.recv)
	})
}

func wait(cmd *exec.Cmd) error {
	return cmd.Wait()
}

func sourceReader(reader io.Reader) Stream[Void, []byte, Unit] {
	return Stream[Void, []byte, Unit](func(env Env[Void, []byte]) (r Result[Unit]) {
		return read(reader, env.send)
	})
}

func sourceWriter(writer io.Writer) Stream[[]byte, Void, Unit] {
	return Stream[[]byte, Void, Unit](func(env Env[[]byte, Void]) (r Result[Unit]) {
		return write(writer, env.recv)
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
				return Value(unit)
			}
		}

		if err == io.EOF {
			return Value(unit)
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

	return Value(unit)
}

func coalesceErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func funcToStream[A, B any](f func(a A) B) Stream[A, B, Unit] {
	return Stream[A, B, Unit](func(env Env[A, B]) Result[Unit] {
		for maybeA := env.recv(); maybeA != nil; maybeA = env.recv() {
			if !env.send(f(*maybeA)) {
				break
			}
		}
		return Value(unit)
	})
}

func mapIn[A, B, C, R any](stream Stream[B, C, R], f func(a A) B) Stream[A, C, R] {
	return Stream[A, C, R](func(env Env[A, C]) Result[R] {
		return stream(Env[B, C]{
			ctx:  env.ctx,
			recv: func() Maybe[B] { return mapMaybe(env.recv(), f) },
			send: env.send,
		})
	})
}

func mapOut[A, B, C, R any](stream Stream[A, B, R], f func(b B) C) Stream[A, C, R] {
	return Stream[A, C, R](func(env Env[A, C]) Result[R] {
		return stream(Env[A, B]{
			ctx:  env.ctx,
			recv: env.recv,
			send: func(b B) bool { return env.send(f(b)) },
		})
	})
}
