package forwarding

import (
	"context"
	"errors"
	"io"
)

type tcpCopyEnd uint8

const (
	tcpCopyEOF tcpCopyEnd = iota
	tcpCopyReadError
	tcpCopyWriteError
	tcpCopyContextDone
)

type tcpCopyResult struct {
	end tcpCopyEnd
	err error
}

// copyTCP copies one direction of a TCP flow. Socket ownership and close
// coordination stay with tcpConn; this function only moves bytes and reports
// why that direction stopped.
func copyTCP(ctx context.Context, dst io.Writer, src io.Reader, limiter *byteRateLimiter, buffer []byte, onProgress func(int)) tcpCopyResult {
	if len(buffer) == 0 {
		return tcpCopyResult{end: tcpCopyReadError, err: errors.New("tcp copy buffer is empty")}
	}
	for {
		select {
		case <-ctx.Done():
			return tcpCopyResult{end: tcpCopyContextDone, err: ctx.Err()}
		default:
		}

		n, readErr := src.Read(buffer)
		if n > 0 {
			if err := limiter.Wait(ctx, n); err != nil {
				return tcpCopyResult{end: tcpCopyContextDone, err: err}
			}
			written, err := writeAll(dst, buffer[:n])
			if written > 0 && onProgress != nil {
				onProgress(written)
			}
			if err != nil {
				return tcpCopyResult{end: tcpCopyWriteError, err: err}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return tcpCopyResult{end: tcpCopyEOF, err: io.EOF}
			}
			return tcpCopyResult{end: tcpCopyReadError, err: readErr}
		}
	}
}

func writeAll(dst io.Writer, buffer []byte) (int, error) {
	written := 0
	for len(buffer) > 0 {
		n, err := dst.Write(buffer)
		if n < 0 || n > len(buffer) {
			return written, errors.New("invalid tcp writer count")
		}
		written += n
		buffer = buffer[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}
