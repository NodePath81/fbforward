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

func TestPolicyDelaysUpstreamUnusableUntilStartupGrace(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 2, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime:          now.Add(-2 * time.Minute),
		StartupGracePeriod: 5 * time.Minute,
		UnusableInterval:   30 * time.Second,
		NotifyInterval:     30 * time.Minute,
		Now:                func() time.Time { return now },
		AfterFunc:          factory.After,
	})

	policy.HandleUsabilityChange("us-1", false, "failover_loss")

	timer := factory.Last()
	if timer == nil || timer.delay != 3*time.Minute {
		t.Fatalf("expected startup-grace timer of 3m, got %#v", timer)
	}

	now = now.Add(3 * time.Minute)
	timer.Fire()

	if len(emitter.events) != 1 {
		t.Fatalf("expected unusable alert after startup grace, got %d", len(emitter.events))
	}
	if emitter.events[0].name != "upstream.unusable" {
		t.Fatalf("unexpected event name %q", emitter.events[0].name)
	}
	if emitter.events[0].severity != SeverityWarn {
		t.Fatalf("unexpected severity %q", emitter.events[0].severity)
	}
	if emitter.events[0].attributes["upstream.tag"] != "us-1" {
		t.Fatalf("unexpected attributes %#v", emitter.events[0].attributes)
	}
	if emitter.events[0].attributes["upstream.reason"] != "failover_loss" {
		t.Fatalf("unexpected attributes %#v", emitter.events[0].attributes)
	}
}

func TestPolicyDelaysUpstreamUnusableUntilThresholdAfterGrace(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 10, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime:          now.Add(-10 * time.Minute),
		StartupGracePeriod: 5 * time.Minute,
		UnusableInterval:   45 * time.Second,
		NotifyInterval:     30 * time.Minute,
		Now:                func() time.Time { return now },
		AfterFunc:          factory.After,
	})

	policy.HandleUsabilityChange("us-1", false, "failover_retrans")

	timer := factory.Last()
	if timer == nil || timer.delay != 45*time.Second {
		t.Fatalf("expected unusable threshold timer of 45s, got %#v", timer)
	}
	timer.Fire()

	if len(emitter.events) != 1 {
		t.Fatalf("expected one unusable event, got %d", len(emitter.events))
	}
	if emitter.events[0].attributes["upstream.reason"] != "failover_retrans" {
		t.Fatalf("unexpected event attributes %#v", emitter.events[0].attributes)
	}
}

func TestPolicyRepeatsUpstreamUnusableAfterNotifyInterval(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 10, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime:          now.Add(-10 * time.Minute),
		StartupGracePeriod: 5 * time.Minute,
		UnusableInterval:   30 * time.Second,
		NotifyInterval:     2 * time.Minute,
		Now:                func() time.Time { return now },
		AfterFunc:          factory.After,
	})

	policy.HandleUsabilityChange("us-1", false, "failover_loss")
	firstTimer := factory.Last()
	firstTimer.Fire()

	if len(emitter.events) != 1 {
		t.Fatalf("expected initial unusable alert, got %d", len(emitter.events))
	}
	repeatTimer := factory.Last()
	if repeatTimer == firstTimer || repeatTimer.delay != 2*time.Minute {
		t.Fatalf("expected repeat timer of 2m, got %#v", repeatTimer)
	}

	now = now.Add(2 * time.Minute)
	repeatTimer.Fire()
	if len(emitter.events) != 2 {
		t.Fatalf("expected repeated unusable alert, got %d", len(emitter.events))
	}
}

func TestPolicyRecoveryResetsUpstreamUnusableEpisode(t *testing.T) {
	now := time.Date(2026, 4, 10, 12, 10, 0, 0, time.UTC)
	emitter := &recordingEmitter{}
	factory := &timerFactory{}
	policy := NewPolicy(emitter, PolicyConfig{
		StartTime:          now.Add(-10 * time.Minute),
		StartupGracePeriod: 5 * time.Minute,
		UnusableInterval:   30 * time.Second,
		NotifyInterval:     2 * time.Minute,
		Now:                func() time.Time { return now },
		AfterFunc:          factory.After,
	})

	policy.HandleUsabilityChange("us-1", false, "failover_loss")
	firstTimer := factory.Last()
	firstTimer.Fire()
	if len(emitter.events) != 1 {
		t.Fatalf("expected initial unusable alert, got %d", len(emitter.events))
	}

	repeatTimer := factory.Last()
	policy.HandleUsabilityChange("us-1", true, "recovered")
	if !repeatTimer.stopped {
		t.Fatalf("expected recovery to stop repeat timer")
	}
	now = now.Add(2 * time.Minute)
	repeatTimer.Fire()
	if len(emitter.events) != 1 {
		t.Fatalf("expected recovery to cancel repeat timer, got %d", len(emitter.events))
	}

	policy.HandleUsabilityChange("us-1", false, "failover_dial")
	resetTimer := factory.Last()
	if resetTimer.delay != 30*time.Second {
		t.Fatalf("expected fresh unusable timer after recovery, got %#v", resetTimer)
	}
	resetTimer.Fire()
	if len(emitter.events) != 2 {
		t.Fatalf("expected a new unusable episode after recovery, got %d", len(emitter.events))
	}
	if emitter.events[1].attributes["upstream.reason"] != "failover_dial" {
		t.Fatalf("unexpected reset event attributes %#v", emitter.events[1].attributes)
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
	if firstTimer == nil || firstTimer.delay != 30*time.Second {
		t.Fatalf("expected coordination disconnect timer of 30s, got %#v", firstTimer)
	}
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
