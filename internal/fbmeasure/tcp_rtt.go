package fbmeasure

import (
	"context"
	"fmt"
	"time"
)

func (c *Client) PingTCP(ctx context.Context, count int) (RTTStats, error) {
	if count <= 0 {
		return RTTStats{}, fmt.Errorf("count must be > 0")
	}

	var acc rttAccumulator
	for seq := 0; seq < count; seq++ {
		start := time.Now()
		req := pingTCPRequest{
			Sequence:          uint64(seq + 1),
			ClientTimestampNs: start.UnixNano(),
		}
		var resp pingTCPResponse
		if err := c.withLockedCall(ctx, opPingTCP, req, nil, func(jsonPayload []byte) error {
			return unmarshalPayload(jsonPayload, &resp)
		}); err != nil {
			return RTTStats{}, err
		}
		if resp.Sequence != req.Sequence || resp.ClientTimestampNs != req.ClientTimestampNs {
			return RTTStats{}, fmt.Errorf("ping_tcp echo mismatch")
		}
		acc.Add(time.Since(start))
	}
	return acc.Stats(), nil
}

func handlePingTCPRequest(req pingTCPRequest) pingTCPResponse {
	return pingTCPResponse{
		Sequence:          req.Sequence,
		ClientTimestampNs: req.ClientTimestampNs,
		ServerTimestampNs: time.Now().UnixNano(),
	}
}
