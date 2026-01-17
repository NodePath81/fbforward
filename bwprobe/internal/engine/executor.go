package engine

import (
	"github.com/NodePath81/fbforward/bwprobe/internal/network"
	"github.com/NodePath81/fbforward/bwprobe/internal/transport"
)

// SampleExecutor abstracts data transfer per sample.
type SampleExecutor interface {
	Transfer(remaining int64) (int, error)
	SetSampleID(sampleID uint32)
	Close() error
}

type senderExecutor struct {
	sender network.Sender
}

func newSenderExecutor(sender network.Sender) SampleExecutor {
	return senderExecutor{sender: sender}
}

func (e senderExecutor) Transfer(remaining int64) (int, error) {
	return e.sender.Send(remaining)
}

func (e senderExecutor) SetSampleID(sampleID uint32) {
	e.sender.SetSampleID(sampleID)
}

func (e senderExecutor) Close() error {
	return e.sender.Close()
}

type receiverExecutor struct {
	receiver transport.Receiver
}

func newReceiverExecutor(receiver transport.Receiver) SampleExecutor {
	return receiverExecutor{receiver: receiver}
}

func (e receiverExecutor) Transfer(remaining int64) (int, error) {
	return e.receiver.Receive(remaining)
}

func (e receiverExecutor) SetSampleID(sampleID uint32) {
	e.receiver.SetSampleID(sampleID)
}

func (e receiverExecutor) Close() error {
	return e.receiver.Close()
}

// SampleStatsProvider surfaces per-sample receive stats.
type SampleStatsProvider interface {
	SampleStats() (recv uint64, lost uint64, bytes uint64)
}

func (e receiverExecutor) SampleStats() (recv uint64, lost uint64, bytes uint64) {
	if stats, ok := e.receiver.(transport.SampleStatsProvider); ok {
		return stats.SampleStats()
	}
	return 0, 0, 0
}
