package metrics

import "testing"

func TestReceiverAddAndStats(t *testing.T) {
	r := &Receiver{}
	seq := []uint64{0, 1, 2, 4, 5, 6}
	for _, s := range seq {
		r.Add(s, 100)
	}
	recv, lost, bytes := r.Stats()
	if recv != uint64(len(seq)) {
		t.Fatalf("recv expected %d, got %d", len(seq), recv)
	}
	if lost != 1 {
		t.Fatalf("lost expected 1, got %d", lost)
	}
	if bytes != uint64(len(seq)*100) {
		t.Fatalf("bytes expected %d, got %d", len(seq)*100, bytes)
	}

	r.Reset()
	if recv, lost, bytes = r.Stats(); recv != 0 || lost != 0 || bytes != 0 {
		t.Fatalf("after reset expected zeros, got %d %d %d", recv, lost, bytes)
	}
}

func TestReceiverDuplicates(t *testing.T) {
	r := &Receiver{}
	r.Add(0, 100)
	r.Add(1, 100)
	r.Add(1, 100) // duplicate

	recv, lost, _ := r.Stats()
	if recv != 3 {
		t.Fatalf("expected recv 3 including duplicate, got %d", recv)
	}
	if lost != 0 {
		t.Fatalf("expected lost 0, got %d", lost)
	}
}
