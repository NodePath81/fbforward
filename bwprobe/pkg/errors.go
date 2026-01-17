package probe

import "errors"

var (
	// ErrInvalidNetwork indicates an unsupported network protocol value.
	ErrInvalidNetwork = errors.New("network must be tcp or udp")
)
