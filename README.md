# `go-streaming`

`go-streaming` is a Go stream processing package inspired by [conduit](https://github.com/snoyberg/conduit) that:

- Makes **reusable/composable** streams that can be connected together like building blocks
- Supports **generics** for extra type safety
- Supports **cancellation** via `context`
- Handles **errors** anywhere in a stream and does a clean shutdown
- Is **pull-based** which guarantees constant memory use

```
go get github.com/chrismwendt/go-streaming
```

# How to build and run streams

Create a source:

- `streaming.SourceReader` takes an `io.Reader` such as `os.Stdin` and turns it into a stream of `[]byte` chunks
- `streaming.SourceSlice` emits each element of a slice `[]T` downstream one by one
- `streaming.SourceExec` streams stdout from a command

Then `streaming.Connect` the source to other streams:

- `streaming.Take` takes N values from upstream and passes them downstream, then stops
- `streaming.FuncToStream` takes a function `func(A) B` and makes a stream that calls it on each element

Then `streaming.Connect` that stream to a sink:

- `streaming.SinkWriter` takes an `io.Writer` such as `os.Stdout` and writes `[]byte` chunks to it
- `streaming.SinkSlice` accumulates all upstream values into a slice `[]T`
- `streaming.SinkExec` streams into the stdin of a command

Finally run the stream with `streaming.Run(ctx, stream)`. That will also return the result of the stream, if relevant (e.g. `streaming.SinkSlice` returns `[]T`).

# Example: `cat`

```go
s1 := streaming.SourceReader(os.Stdin)
s2 := streaming.Connect(s1, streaming.SinkWriter(os.Stdout))
result := streaming.Run(context.Background(), s2)
if result.Error != nil {
  panic(result.Error)
}
```

# Example: `head -n 5`

```go
s1 := streaming.SourceReader(os.Stdin)
s2 := streaming.Connect(s1, streaming.SplitOn("\n"))
s3 := streaming.Connect(s2, streaming.Take(5))
s4 := streaming.Connect(s3, streaming.SinkWriter(os.Stdout))
result := streaming.Run(context.Background(), s4)
if result.Error != nil {
  panic(result.Error)
}
```

# Example: find the maximum line length

```go
s1 := streaming.SourceReader(os.Stdin)
s2 := streaming.Connect(s1, streaming.Concat[byte]())
s3 := streaming.Connect(s2, streaming.SplitOn(byte('\n')))

length := func(ctx context.Context, stream streaming.Stream[streaming.Void, byte, streaming.Unit]) streaming.Result[int] {
  return streaming.Run(ctx, streaming.Connect(stream, streaming.Count[byte]()))
}

s4 := streaming.Connect(s3, streaming.FuncToStreamCtx(length))
s5 := streaming.Connect(s4, streaming.Max())
result := streaming.Run(context.Background(), s5)
if result.Error != nil {
  panic(result.Error)
}
maybeSum := *result.Value
if maybeSum == nil {
  fmt.Println("input contains no lines")
}
sum := *maybeSum
fmt.Println("Max line length:", sum)
```

# Alternatives

- [github.com/reugn/go-streams](https://github.com/reugn/go-streams) doesn't support generics or cancellation, but has built-in parallelism
