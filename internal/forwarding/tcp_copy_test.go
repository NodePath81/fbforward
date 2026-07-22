package forwarding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestTCPCopy(t *testing.T) {
	tests := []struct {
		name    string
		source  io.Reader
		want    string
		wantEnd tcpCopyEnd
		wantErr error
	}{
		{name: "complete", source: bytes.NewBufferString("payload"), want: "payload", wantEnd: tcpCopyEOF, wantErr: io.EOF},
		{name: "short reads", source: &shortReader{data: []byte("fragmented")}, want: "fragmented", wantEnd: tcpCopyEOF, wantErr: io.EOF},
		{name: "partial data with eof", source: &dataEOFReader{data: []byte("last")}, want: "last", wantEnd: tcpCopyEOF, wantErr: io.EOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var destination bytes.Buffer
			progress := 0
			result := copyTCP(context.Background(), &destination, tt.source, nil, make([]byte, 4), func(n int) { progress += n })
			if result.end != tt.wantEnd || !errors.Is(result.err, tt.wantErr) {
				t.Fatalf("result = %+v, want end=%v err=%v", result, tt.wantEnd, tt.wantErr)
			}
			if got := destination.String(); got != tt.want {
				t.Fatalf("destination = %q, want %q", got, tt.want)
			}
			if progress != len(tt.want) {
				t.Fatalf("progress = %d, want %d", progress, len(tt.want))
			}
		})
	}
}

func TestTCPCopyErrors(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		want := errors.New("read failed")
		result := copyTCP(context.Background(), io.Discard, errorReader{err: want}, nil, make([]byte, 8), nil)
		if result.end != tcpCopyReadError || !errors.Is(result.err, want) {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("write", func(t *testing.T) {
		want := errors.New("write failed")
		result := copyTCP(context.Background(), errorWriter{err: want}, bytes.NewBufferString("x"), nil, make([]byte, 8), nil)
		if result.end != tcpCopyWriteError || !errors.Is(result.err, want) {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("partial write", func(t *testing.T) {
		want := errors.New("write failed")
		progress := 0
		result := copyTCP(context.Background(), partialErrorWriter{written: 2, err: want}, bytes.NewBufferString("data"), nil, make([]byte, 8), func(n int) {
			progress += n
		})
		if result.end != tcpCopyWriteError || !errors.Is(result.err, want) {
			t.Fatalf("result = %+v", result)
		}
		if progress != 2 {
			t.Fatalf("progress = %d, want 2", progress)
		}
	})
	t.Run("zero progress", func(t *testing.T) {
		result := copyTCP(context.Background(), zeroProgressWriter{}, bytes.NewBufferString("x"), nil, make([]byte, 8), nil)
		if result.end != tcpCopyWriteError || !errors.Is(result.err, io.ErrShortWrite) {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("empty buffer", func(t *testing.T) {
		result := copyTCP(context.Background(), io.Discard, bytes.NewBufferString("x"), nil, nil, nil)
		if result.end != tcpCopyReadError || result.err == nil {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestTCPCopyRateWaitCancellation(t *testing.T) {
	limiter := newByteRateLimiter(8)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("consume initial limiter burst")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := copyTCP(ctx, io.Discard, &cancelingReader{reader: bytes.NewBufferString("x"), cancel: cancel}, limiter, make([]byte, 8), nil)
	if result.end != tcpCopyContextDone || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("result = %+v", result)
	}
}

func BenchmarkTCPCopy(b *testing.B) {
	for _, size := range []int{1024, 32 * 1024, 1024 * 1024} {
		b.Run(byteSizeName(size), func(b *testing.B) {
			payload := bytes.Repeat([]byte{'x'}, size)
			buffer := make([]byte, tcpBufferSize)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := copyTCP(context.Background(), io.Discard, bytes.NewReader(payload), nil, buffer, nil)
				if result.end != tcpCopyEOF {
					b.Fatalf("copy end = %v", result.end)
				}
			}
		})
	}
	b.Run("Parallel32K", func(b *testing.B) {
		payload := bytes.Repeat([]byte{'x'}, 32*1024)
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			buffer := make([]byte, tcpBufferSize)
			for pb.Next() {
				result := copyTCP(context.Background(), io.Discard, bytes.NewReader(payload), nil, buffer, func(int) {})
				if result.end != tcpCopyEOF {
					b.Fatalf("copy end = %v", result.end)
				}
			}
		})
	})
}

func byteSizeName(size int) string {
	switch size {
	case 1024:
		return "1K"
	case 32 * 1024:
		return "32K"
	case 1024 * 1024:
		return "1M"
	default:
		return "unknown"
	}
}

type shortReader struct{ data []byte }

func (r *shortReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

type dataEOFReader struct{ data []byte }

func (r *dataEOFReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = nil
	return n, io.EOF
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

type partialErrorWriter struct {
	written int
	err     error
}

func (w partialErrorWriter) Write(p []byte) (int, error) {
	return min(w.written, len(p)), w.err
}

type zeroProgressWriter struct{}

func (zeroProgressWriter) Write([]byte) (int, error) { return 0, nil }

type cancelingReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r *cancelingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.cancel()
	return n, err
}
