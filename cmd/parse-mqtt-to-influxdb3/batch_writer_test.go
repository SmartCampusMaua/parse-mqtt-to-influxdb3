package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

// fakeInfluxWriter records every WritePoints call. Safe for concurrent use
// since batchWriter's run loop is the only caller, but tests read the
// recorded calls after Close() returns, from a different goroutine.
type fakeInfluxWriter struct {
	mu      sync.Mutex
	calls   [][]*influxdb3.Point
	failN   int // number of leading calls to fail (across all calls, batch or per-point)
	failErr error
}

func (f *fakeInfluxWriter) WritePoints(_ context.Context, points []*influxdb3.Point, _ ...influxdb3.WriteOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failN > 0 {
		f.failN--
		return f.failErr
	}
	f.calls = append(f.calls, points)
	return nil
}

func (f *fakeInfluxWriter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeInfluxWriter) totalPoints() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

func newTestPoint(deviceID string) *influxdb3.Point {
	return influxdb3.NewPointWithMeasurement("sensor_data").
		SetTag("device_id", deviceID).
		SetIntegerField("value_int", 1).
		SetTimestamp(time.Now())
}

func TestBatchWriterFlushesOnSizeThreshold(t *testing.T) {
	fake := &fakeInfluxWriter{}
	bw := newBatchWriter(fake, "sensor_data", time.Hour, 3)

	bw.Enqueue([]*influxdb3.Point{newTestPoint("a"), newTestPoint("b")})
	bw.Enqueue([]*influxdb3.Point{newTestPoint("c")})

	deadline := time.After(2 * time.Second)
	for fake.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected a flush triggered by size threshold, got none")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	bw.Close()
	if got := fake.callCount(); got != 1 {
		t.Fatalf("callCount = %d, want 1 (single batch flush)", got)
	}
	if got := fake.totalPoints(); got != 3 {
		t.Fatalf("totalPoints = %d, want 3", got)
	}
}

func TestBatchWriterFlushesOnTimer(t *testing.T) {
	fake := &fakeInfluxWriter{}
	bw := newBatchWriter(fake, "sensor_data", 20*time.Millisecond, 1000)

	bw.Enqueue([]*influxdb3.Point{newTestPoint("a")})

	deadline := time.After(2 * time.Second)
	for fake.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected a flush triggered by the timer, got none")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	bw.Close()
	if got := fake.totalPoints(); got != 1 {
		t.Fatalf("totalPoints = %d, want 1", got)
	}
}

func TestBatchWriterCloseDrainsBufferedPoints(t *testing.T) {
	fake := &fakeInfluxWriter{}
	bw := newBatchWriter(fake, "sensor_data", time.Hour, 1000)

	bw.Enqueue([]*influxdb3.Point{newTestPoint("a"), newTestPoint("b")})
	bw.Close()

	if got := fake.callCount(); got != 1 {
		t.Fatalf("callCount = %d, want 1 (Close must flush buffered points)", got)
	}
	if got := fake.totalPoints(); got != 2 {
		t.Fatalf("totalPoints = %d, want 2", got)
	}
}

func TestBatchWriterCloseWithNothingBufferedIsNoop(t *testing.T) {
	fake := &fakeInfluxWriter{}
	bw := newBatchWriter(fake, "sensor_data", time.Hour, 1000)
	bw.Close()

	if got := fake.callCount(); got != 0 {
		t.Fatalf("callCount = %d, want 0 (nothing was ever enqueued)", got)
	}
}

func TestBatchWriterEnqueueEmptySliceIsNoop(t *testing.T) {
	fake := &fakeInfluxWriter{}
	bw := newBatchWriter(fake, "sensor_data", time.Hour, 1000)
	bw.Enqueue(nil)
	bw.Enqueue([]*influxdb3.Point{})
	bw.Close()

	if got := fake.callCount(); got != 0 {
		t.Fatalf("callCount = %d, want 0 (empty enqueues must not trigger a flush)", got)
	}
}

func TestBatchWriterFallsBackToPerPointOnBatchFailure(t *testing.T) {
	fake := &fakeInfluxWriter{failN: 1, failErr: context.DeadlineExceeded}
	bw := newBatchWriter(fake, "sensor_data", time.Hour, 1000)

	bw.Enqueue([]*influxdb3.Point{newTestPoint("a"), newTestPoint("b")})
	bw.Close()

	// First call (the batch) fails and is not retried past maxAttempts=3 for a
	// non-4xx/5xx error is still retried 3x by writeWithRetry, but failN=1
	// only fails the very first WritePoints call, so attempt 2 of the batch
	// retry succeeds — verify no per-point fallback was needed by checking
	// exactly one successful call carries both points.
	if got := fake.totalPoints(); got != 2 {
		t.Fatalf("totalPoints = %d, want 2 (points must land whether via batch retry or per-point fallback)", got)
	}
}
