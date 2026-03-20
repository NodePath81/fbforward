package fbmeasure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const (
	opPingTCP    = "ping_tcp"
	opPingUDP    = "ping_udp"
	opTCPRetrans = "tcp_retrans"
	opUDPLoss    = "udp_loss"

	udpPacketKindPing = 1
	udpPacketKindPong = 2
	udpPacketKindLoss = 3

	tcpDataMarker = "\xffFMT"
	testIDSize    = 16

	udpAuthKeySize    = 32
	udpAuthTagSize    = 16
	udpPingHeaderSize = 1 + testIDSize + 8 + 8
	udpLossHeaderSize = 1 + testIDSize + 8
	udpPingPacketSize = udpPingHeaderSize + udpAuthTagSize
	udpLossMinSize    = udpLossHeaderSize + udpAuthTagSize

	defaultAuxStartDelay = 10 * time.Millisecond
)

type TestID [testIDSize]byte

func newTestID() (TestID, error) {
	var id TestID
	if _, err := rand.Read(id[:]); err != nil {
		return TestID{}, err
	}
	return id, nil
}

func parseTestID(raw string) (TestID, error) {
	var id TestID
	if len(raw) != testIDSize*2 {
		return TestID{}, fmt.Errorf("invalid test_id length")
	}
	buf, err := hex.DecodeString(raw)
	if err != nil {
		return TestID{}, fmt.Errorf("decode test_id: %w", err)
	}
	copy(id[:], buf)
	return id, nil
}

func (id TestID) String() string {
	return hex.EncodeToString(id[:])
}

type controlRequest struct {
	ID      uint64          `json:"id"`
	Op      string          `json:"op"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type controlResponse struct {
	ID      uint64          `json:"id"`
	Op      string          `json:"op"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type pingTCPRequest struct {
	Sequence          uint64 `json:"sequence"`
	ClientTimestampNs int64  `json:"client_timestamp_ns"`
}

type pingTCPResponse struct {
	Sequence          uint64 `json:"sequence"`
	ClientTimestampNs int64  `json:"client_timestamp_ns"`
	ServerTimestampNs int64  `json:"server_timestamp_ns"`
}

type pingUDPRequest struct {
	TestID    string `json:"test_id"`
	AuthKey   string `json:"auth_key"`
	Count     int    `json:"count"`
	TimeoutMs int    `json:"timeout_ms"`
}

type pingUDPResponse struct {
	TestID   string `json:"test_id"`
	Received int    `json:"received"`
}

type tcpRetransRequest struct {
	TestID    string `json:"test_id"`
	Bytes     uint64 `json:"bytes"`
	TimeoutMs int    `json:"timeout_ms"`
}

type tcpRetransResponse struct {
	TestID       string `json:"test_id"`
	BytesSent    uint64 `json:"bytes_sent"`
	Retransmits  uint64 `json:"retransmits"`
	SegmentsSent uint64 `json:"segments_sent"`
	RTTNs        int64  `json:"rtt_ns"`
	RTTVarNs     int64  `json:"rtt_var_ns"`
}

type udpLossRequest struct {
	TestID     string `json:"test_id"`
	AuthKey    string `json:"auth_key"`
	Packets    int    `json:"packets"`
	PacketSize int    `json:"packet_size"`
	TimeoutMs  int    `json:"timeout_ms"`
}

type udpLossResponse struct {
	TestID      string  `json:"test_id"`
	PacketsSent uint64  `json:"packets_sent"`
	PacketsRecv uint64  `json:"packets_recv"`
	PacketsLost uint64  `json:"packets_lost"`
	OutOfOrder  uint64  `json:"out_of_order"`
	LossRate    float64 `json:"loss_rate"`
}

func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func unmarshalPayload[T any](raw json.RawMessage, dst *T) error {
	if len(raw) == 0 {
		return fmt.Errorf("missing payload")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return err
	}
	return nil
}

func writeControlMessage(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxControlMessageSize {
		return fmt.Errorf("invalid control message size: %d", len(data))
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}

func readControlMessage(r io.Reader, dst any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(prefix[:])
	if size == 0 || size > maxControlMessageSize {
		return fmt.Errorf("invalid control message size: %d", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	if err := json.Unmarshal(buf, dst); err != nil {
		return err
	}
	return nil
}

func appendTestID(dst []byte, id TestID) []byte {
	return append(dst, id[:]...)
}

func newUDPAuthKey() ([udpAuthKeySize]byte, error) {
	var key [udpAuthKeySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		return key, err
	}
	return key, nil
}

func parseUDPAuthKey(raw string) ([udpAuthKeySize]byte, error) {
	var key [udpAuthKeySize]byte
	if len(raw) != udpAuthKeySize*2 {
		return key, fmt.Errorf("invalid auth_key length")
	}
	buf, err := hex.DecodeString(raw)
	if err != nil {
		return key, fmt.Errorf("decode auth_key: %w", err)
	}
	copy(key[:], buf)
	return key, nil
}

func udpAuthKeyString(key [udpAuthKeySize]byte) string {
	return hex.EncodeToString(key[:])
}

func appendUDPAuthTag(dst []byte, key [udpAuthKeySize]byte) []byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(dst)
	sum := mac.Sum(nil)
	return append(dst, sum[:udpAuthTagSize]...)
}

func verifyUDPAuthTag(data []byte, key [udpAuthKeySize]byte) bool {
	if len(data) < udpAuthTagSize {
		return false
	}
	body := data[:len(data)-udpAuthTagSize]
	tag := data[len(data)-udpAuthTagSize:]
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	return subtle.ConstantTimeCompare(tag, expected[:udpAuthTagSize]) == 1
}
