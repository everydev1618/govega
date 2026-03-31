package vega

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFutureConcurrentAccess(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	var wg sync.WaitGroup

	// Multiple goroutines checking Done
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Done()
		}()
	}

	// Complete the future
	go func() {
		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		f.result = "done"
		f.completed = true
		f.mu.Unlock()
		close(f.done)
	}()

	// Multiple goroutines awaiting
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			f.Await(ctx)
		}()
	}

	wg.Wait()
}

func TestFutureResultWithError(t *testing.T) {
	f := &Future{
		done:      make(chan struct{}),
		cancel:    make(chan struct{}),
		completed: true,
		err:       ErrTimeout,
		result:    "",
	}

	result, err := f.Result()
	if err != ErrTimeout {
		t.Errorf("Future.Result() err = %v, want ErrTimeout", err)
	}
	if result != "" {
		t.Errorf("Future.Result() = %q, want empty", result)
	}
}

func TestFutureCancelIdempotent(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}, 1), // buffered to not block
	}

	// Cancel multiple times should not panic
	f.Cancel()
	f.Cancel()
	f.Cancel()
}

func TestFutureAwaitWithCancelledContext(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := f.Await(ctx)
	if err != context.Canceled {
		t.Errorf("Await with cancelled context = %v, want context.Canceled", err)
	}
}

func TestStreamWaitForDone(t *testing.T) {
	s := &Stream{
		chunks: make(chan string),
		done:   make(chan struct{}),
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
		s.response = "delayed response"
		s.mu.Unlock()
		close(s.done)
	}()

	// Response() should block until done
	resp := s.Response()
	if resp != "delayed response" {
		t.Errorf("Response() = %q, want %q", resp, "delayed response")
	}
}

func TestStreamErrWaitForDone(t *testing.T) {
	s := &Stream{
		chunks: make(chan string),
		done:   make(chan struct{}),
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
		s.err = ErrBudgetExceeded
		s.mu.Unlock()
		close(s.done)
	}()

	err := s.Err()
	if err != ErrBudgetExceeded {
		t.Errorf("Err() = %v, want ErrBudgetExceeded", err)
	}
}
