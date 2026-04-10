package notify

import (
	"testing"
	"time"
)

type recordedEvent struct {
	name       string
	severity   Severity
	attributes map[string]any
}

type recordingEmitter struct {
	events []recordedEvent
}

func (e *recordingEmitter) Emit(eventName string, severity Severity, attributes map[string]any) bool {
	cloned := make(map[string]any, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	e.events = append(e.events, recordedEvent{
		name:       eventName,
		severity:   severity,
		attributes: cloned,
	})
	return true
}

type manualTimer struct {
	delay   time.Duration
	fn      func()
	stopped bool
}

func (t *manualTimer) Stop() bool {
	wasStopped := t.stopped
	t.stopped = true
	return !wasStopped
}

func (t *manualTimer) Fire() {
	if t.stopped {
		return
	}
	t.fn()
}

type timerFactory struct {
	timers []*manualTimer
}

func (f *timerFactory) After(delay time.Duration, fn func()) timer {
	timer := &manualTimer{
		delay: delay,
		fn:    fn,
	}
	f.timers = append(f.timers, timer)
	return timer
}

func (f *timerFactory) Last() *manualTimer {
	if len(f.timers) == 0 {
		return nil
	}
	return f.timers[len(f.timers)-1]
}

func TestPolicyEmitsOnlyFailoverReasons(t *testing.T) {
	emitter := &recordingEmitter{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})

	policy.HandleActiveChange("a", "b", "score_delta", 0, 0)
	policy.HandleActiveChange("b", "c", "failover_dial", 0, 0)

	if len(emitter.events) != 1 {
		t.Fatalf("expected one failover event, got %d", len(emitter.events))
	}
	if emitter.events[0].name != "upstream.active_changed" {
		t.Fatalf("unexpected event name %q", emitter.events[0].name)
	}
	if emitter.events[0].attributes["switch.reason"] != "failover_dial" {
		t.Fatalf("unexpected switch reason %#v", emitter.events[0].attributes)
	}
}

func TestPolicyDelaysSustainedOutageUntilQuietPeriod(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 2, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime: now.Add(-2 * time.Minute),
		Now:       func() time.Time { return now },
		AfterFunc: factory.After,
	})

	policy.HandleActiveChange("a", "", "failover_loss", 0, 0)

	timer := factory.Last()
	if timer == nil || timer.delay != 3*time.Minute {
		t.Fatalf("expected quiet-period timer of 3m, got %#v", timer)
	}
	now = now.Add(3 * time.Minute)
	timer.Fire()

	if len(emitter.events) != 1 {
		t.Fatalf("expected outage alert after quiet period, got %d", len(emitter.events))
	}
	if emitter.events[0].attributes["notification.state"] != "active" {
		t.Fatalf("unexpected outage attributes %#v", emitter.events[0].attributes)
	}
}

func TestPolicyCancelsAndResolvesOutageAlerts(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 10, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime: now.Add(-10 * time.Minute),
		Now:       func() time.Time { return now },
		AfterFunc: factory.After,
	})

	policy.HandleActiveChange("a", "", "failover_loss", 0, 0)
	factory.Last().Fire()
	policy.HandleUsabilityChange("a", true, "recovered")

	if len(emitter.events) != 2 {
		t.Fatalf("expected alert and recovery, got %d", len(emitter.events))
	}
	if emitter.events[0].severity != SeverityCritical {
		t.Fatalf("unexpected alert severity %q", emitter.events[0].severity)
	}
	if emitter.events[1].severity != SeverityInfo {
		t.Fatalf("unexpected recovery severity %q", emitter.events[1].severity)
	}
	if emitter.events[1].attributes["notification.state"] != "resolved" {
		t.Fatalf("unexpected recovery attributes %#v", emitter.events[1].attributes)
	}
}

func TestPolicyHandlesSustainedCoordinationDisconnect(t *testing.T) {
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime:            time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		CoordinationEndpoint: "https://fbcoord.example",
		AfterFunc:            factory.After,
	})

	policy.HandleCoordinationConnection(true)
	policy.HandleCoordinationConnection(false)
	firstTimer := factory.Last()
	policy.HandleCoordinationConnection(true)
	firstTimer.Fire()

	if len(emitter.events) != 0 {
		t.Fatalf("expected reconnect to cancel disconnect alert, got %d", len(emitter.events))
	}

	policy.HandleCoordinationConnection(false)
	factory.Last().Fire()

	if len(emitter.events) != 1 {
		t.Fatalf("expected one coordination alert, got %d", len(emitter.events))
	}
	if emitter.events[0].name != "coordination.session_ended" {
		t.Fatalf("unexpected event name %q", emitter.events[0].name)
	}
	if emitter.events[0].attributes["coordination.endpoint"] != "https://fbcoord.example" {
		t.Fatalf("unexpected coordination attributes %#v", emitter.events[0].attributes)
	}
}
