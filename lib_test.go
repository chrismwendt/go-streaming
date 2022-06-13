package streaming

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestStreaming(t *testing.T) {
	type Test struct {
		want   []int
		stream func() Stream[Void, Void, []int]
	}

	tests := map[string]Test{
		"empty": {want: []int{}, stream: func() Stream[Void, Void, []int] {
			s1 := sourceSlice([]int{})
			s2 := connect(s1, sinkSlice[int]())
			return s2
		}},
		"simple": {want: []int{1, 2, 3}, stream: func() Stream[Void, Void, []int] {
			s1 := sourceSlice([]int{1, 2, 3})
			s2 := connect(s1, sinkSlice[int]())
			return s2
		}},
		"take less": {want: []int{1}, stream: func() Stream[Void, Void, []int] {
			s1 := sourceSlice([]int{1, 2})
			s2 := connect(s1, take[int](1))
			s3 := connect(s2, sinkSlice[int]())
			return s3
		}},
		"take more": {want: []int{1, 2}, stream: func() Stream[Void, Void, []int] {
			s1 := sourceSlice([]int{1, 2})
			s2 := connect(s1, take[int](3))
			s3 := connect(s2, sinkSlice[int]())
			return s3
		}},
		"leftovers": {want: []int{}, stream: func() Stream[Void, Void, []int] {
			s1 := sourceSlice([]int{1, 2})
			s2 := connect(s1, sinkNull[int]([]int{}))
			return s2
		}},
	}
	for _, test := range tests {
		r := run(context.Background(), test.stream())
		if r.Error != nil {
			t.Fatalf("error %s", *r.Error)
		}
		assertEqual(t, test.want, *r.Value)
	}
}

func TestError(t *testing.T) {
	fooError := errors.New("foo")

	s1 := Stream[Void, Void, Void](func(p Env[Void, Void]) Result[Void] {
		return Error[Void](fooError)
	})
	s2 := connect(s1, sinkNull[Void, Void](nil))

	r := run(context.Background(), s2)

	if r.Error == nil {
		t.Fatalf("expected an error")
	}

	if *r.Error != fooError {
		t.Fatalf("expected foo error")
	}
}

func TestExit(t *testing.T) {
	done := make(chan Unit)

	s1 := Stream[Void, int, Unit](func(env Env[Void, int]) Result[Unit] {
		defer func() {
			done <- unit
		}()
		if !env.send(0) {
			return Value(unit)
		}
		return Value(unit)
	})
	s2 := connect(s1, Stream[int, Void, Unit](func(env Env[int, Void]) Result[Unit] {
		if env.recv() == nil {
			t.Fatalf("expected to receive a value")
		}
		return Value(unit)
	}))

	run(context.Background(), s2)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out")
	}
}

func TestExec(t *testing.T) {
	s1 := sourceExec(func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "echo", "-n", "a b c") })
	s2 := connect(s1, mapStream(func(bs []byte) string { return string(bs) }))
	s3 := connect(s2, sinkString())

	r := run(context.Background(), s3)

	if r.Error != nil {
		t.Fatalf("error %s", *r.Error)
	}

	if *r.Value != "a b c" {
		t.Fatalf("expected \"a b c\", got %q", *r.Value)
	}
}

func assertEqual[T comparable](t *testing.T, want []T, got []T) {
	if len(want) != len(got) {
		t.Fatalf("bad lengths, want %d got %d", len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("bad ix %d want %v got %v", i, want[i], got[i])
		}
	}
}
