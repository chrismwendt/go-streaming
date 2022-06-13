package streaming

import "context"

type Void *struct{}

type Unit struct{}

var unit = Unit(struct{}{})

type Maybe[T any] *T

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

func sourceSlice[T any](ts []T) Stream[Void, T, Void] {
	return Stream[Void, T, Void](func(env Env[Void, T]) Result[Void] {
		for _, t := range ts {
			if !env.send(t) {
				break
			}
		}
		return Value[Void](nil)
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

func sinkNull[T, R any](r R) Stream[T, Void, R] {
	return Stream[T, Void, R](func(env Env[T, Void]) Result[R] {
		for env.recv() != nil {
		}
		return Value(r)
	})
}

func take[T any](n int) Stream[T, T, Void] {
	return Stream[T, T, Void](func(env Env[T, T]) Result[Void] {
		for i := 0; i < n; i++ {
			v := env.recv()
			if v == nil {
				break
			}
			if !env.send(*v) {
				break
			}
		}
		return Value[Void](nil)
	})
}
