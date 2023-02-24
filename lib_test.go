package streaming

import (
	"context"
	"errors"
	"os/exec"
	"strings"
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
			s1 := SourceSlice([]int{})
			s2 := Connect(s1, SinkSlice[int]())
			return s2
		}},
		"simple": {want: []int{1, 2, 3}, stream: func() Stream[Void, Void, []int] {
			s1 := SourceSlice([]int{1, 2, 3})
			s2 := Connect(s1, SinkSlice[int]())
			return s2
		}},
		"take less": {want: []int{1}, stream: func() Stream[Void, Void, []int] {
			s1 := SourceSlice([]int{1, 2})
			s2 := Connect(s1, Take[int](1))
			s3 := Connect(s2, SinkSlice[int]())
			return s3
		}},
		"take more": {want: []int{1, 2}, stream: func() Stream[Void, Void, []int] {
			s1 := SourceSlice([]int{1, 2})
			s2 := Connect(s1, Take[int](3))
			s3 := Connect(s2, SinkSlice[int]())
			return s3
		}},
		"leftovers": {want: []int{}, stream: func() Stream[Void, Void, []int] {
			s1 := SourceSlice([]int{1, 2})
			s2 := Connect(s1, SinkNull[int]([]int{}))
			return s2
		}},
	}
	for _, test := range tests {
		r := Run(context.Background(), test.stream())
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
	s2 := Connect(s1, SinkNull[Void, Void](nil))

	r := Run(context.Background(), s2)

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
			done <- unitValue
		}()
		if !env.Send(0) {
			return Value(unitValue)
		}
		return Value(unitValue)
	})
	s2 := Connect(s1, Stream[int, Void, Unit](func(env Env[int, Void]) Result[Unit] {
		if env.Recv() == nil {
			t.Fatalf("expected to receive a value")
		}
		return Value(unitValue)
	}))

	Run(context.Background(), s2)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out")
	}
}

func TestExec(t *testing.T) {
	s1 := SourceExec(func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "echo", "-n", "a b c") }, func(cmd *exec.Cmd) error { return cmd.Wait() })
	s2 := Connect(s1, FuncToStreamPure(func(bs []byte) string { return string(bs) }))
	s3 := Connect(s2, SinkString())

	r := Run(context.Background(), s3)

	if r.Error != nil {
		t.Fatalf("error %s", *r.Error)
	}

	if *r.Value != "a b c" {
		t.Fatalf("expected \"a b c\", got %q", *r.Value)
	}
}

func TestSplit(t *testing.T) {
	tests := map[string]string{
		"empty":                "",
		"1 chunk":              "a",
		"2 chunks":             "a|b",
		"delimiter alone":      "|",
		"delimiter then chunk": "|a",
		"chunk then delimiter": "a|",
	}
	for _, test := range tests {
		s1 := SourceSlice([]byte(test))
		s2 := Connect(s1, SplitBy(func(i byte) bool { return i == '|' }))
		s3 := Connect(s2, FuncToStreamCtx(func(ctx context.Context, s Stream[Void, byte, Unit]) Result[string] {
			return MapValue(func(bs []byte) string { return string(bs) }, Run(ctx, Connect(s, SinkSlice[byte]())))
		}))
		s4 := Connect(s3, SinkSlice[string]())

		r := Run(context.Background(), s4)

		if r.Error != nil {
			t.Fatalf("error %s", *r.Error)
		}

		assertEqual(t, strings.Split(test, "|"), *r.Value)
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
