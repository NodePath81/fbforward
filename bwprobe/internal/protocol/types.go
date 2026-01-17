package protocol

const (
	TCPFrameHeaderSize = 8
	TCPPingHeader      = "PING"
	TCPPongHeader      = "PONG"
	TCPControlHeader   = "CTRL"
	TCPDataHeader      = "DATA"
	TCPReverseHeader   = "RECV"
)

const (
	UDPTypeData        = 1
	UDPTypeDataSession = 6
	UDPTypePing        = 2
	UDPTypePong        = 3
	UDPTypeDone        = 4
	UDPTypeStats       = 5

	UDPSeqHeaderSize    = 1 + 4 + 8
	UDPSessionHeaderMin = 1 + 1 + 4 + 8
	UDPDoneHeaderSize   = 1 + 4
	UDPStatsSize        = 1 + 8 + 8 + 8
	UDPMaxChunkSize     = 64 * 1024
)
