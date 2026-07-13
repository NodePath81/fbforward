package measure

import (
	"math/rand"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestSchedulerInitialJobsAreImmediate(t *testing.T) {
	managerUpstream := &upstream.Upstream{Tag: "primary"}
	scheduler := NewScheduler(SchedulerConfig{
		MinInterval: time.Minute,
		MaxInterval: time.Minute,
		Protocols:   []string{"tcp", "udp"},
	}, []*upstream.Upstream{managerUpstream}, rand.New(rand.NewSource(1)))

	scheduler.Schedule()
	first, ok := scheduler.NextReady()
	if !ok || first.upstream != managerUpstream {
		t.Fatalf("expected an immediate first job, got %#v, ready=%v", first, ok)
	}
	second, ok := scheduler.NextReady()
	if !ok || second.upstream != managerUpstream {
		t.Fatalf("expected the second protocol job, got %#v, ready=%v", second, ok)
	}
}

func TestSchedulerMarkRunSchedulesOneConfiguredInterval(t *testing.T) {
	up := &upstream.Upstream{Tag: "primary"}
	scheduler := NewScheduler(SchedulerConfig{
		MinInterval: time.Minute,
		MaxInterval: time.Minute,
		Protocols:   []string{"tcp"},
	}, []*upstream.Upstream{up}, rand.New(rand.NewSource(1)))

	scheduler.Schedule()
	job, ok := scheduler.NextReady()
	if !ok {
		t.Fatal("expected initial job")
	}
	before := time.Now()
	scheduler.MarkRun(*job)
	status := scheduler.Status()
	if status.QueueLength != 1 || len(status.LastRun) != 1 {
		t.Fatalf("unexpected scheduler status: %+v", status)
	}
	if status.NextScheduled.Before(before.Add(time.Minute)) {
		t.Fatalf("next job scheduled too early: %s", status.NextScheduled.Sub(before))
	}
}

func TestSchedulerRetryDoesNotAddAnExtraInterval(t *testing.T) {
	up := &upstream.Upstream{Tag: "primary"}
	scheduler := NewScheduler(SchedulerConfig{
		MinInterval: time.Hour,
		MaxInterval: time.Hour,
		Protocols:   []string{"tcp"},
	}, []*upstream.Upstream{up}, rand.New(rand.NewSource(1)))

	scheduler.Schedule()
	job, ok := scheduler.NextReady()
	if !ok {
		t.Fatal("expected initial job")
	}
	before := time.Now()
	scheduler.Requeue(*job, 30*time.Second)
	status := scheduler.Status()
	if status.QueueLength != 1 {
		t.Fatalf("expected one retry job, got %+v", status)
	}
	if status.NextScheduled.Before(before.Add(30 * time.Second)) {
		t.Fatalf("retry scheduled too early: %s", status.NextScheduled.Sub(before))
	}
}

func TestSchedulerHonorsInterUpstreamGap(t *testing.T) {
	up := &upstream.Upstream{Tag: "primary"}
	scheduler := NewScheduler(SchedulerConfig{
		MinInterval:      time.Minute,
		MaxInterval:      time.Minute,
		InterUpstreamGap: time.Hour,
		Protocols:        []string{"tcp", "udp"},
	}, []*upstream.Upstream{up}, rand.New(rand.NewSource(1)))

	scheduler.Schedule()
	job, ok := scheduler.NextReady()
	if !ok {
		t.Fatal("expected first job")
	}
	if next, ready := scheduler.NextReady(); ready || next != nil {
		t.Fatalf("expected gap to block second job, got %#v", next)
	}
	if job.protocol == "" {
		t.Fatal("expected protocol-specific job")
	}
}
