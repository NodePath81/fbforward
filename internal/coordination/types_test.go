package coordination

import (
	"encoding/json"
	"testing"
)

func TestHelloMessageMarshalsMinimalPayload(t *testing.T) {
	data, err := json.Marshal(HelloMessage{Type: "hello"})
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if string(data) != `{"type":"hello"}` {
		t.Fatalf("unexpected hello payload: %s", data)
	}
}

func TestByeMessageMarshalsMinimalPayload(t *testing.T) {
	data, err := json.Marshal(ByeMessage{Type: "bye"})
	if err != nil {
		t.Fatalf("marshal bye: %v", err)
	}
	if string(data) != `{"type":"bye"}` {
		t.Fatalf("unexpected bye payload: %s", data)
	}
}
