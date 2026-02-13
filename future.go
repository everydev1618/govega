package vega

import (
	"context"
	"sync"
)

// Future represents an asynchronous operation result.
type Future struct {
	result    string
	err       error
	completed bool
	done      chan struct{}
	cancel    chan struct{}
	mu        sync.RWMutex
}

// Await waits for the future to complete and returns the result.
func (f *Future) Await(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-f.done:
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.result, f.err
	}
}

// Done returns true if the future has completed.
func (f *Future) Done() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.completed
}

// Result returns the result if completed, or error if not.
func (f *Future) Result() (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.completed {
		return "", ErrNotCompleted
	}
	return f.result, f.err
}

// Cancel cancels the future.
func (f *Future) Cancel() {
	select {
	case f.cancel <- struct{}{}:
	default:
	}
}

// Stream represents a streaming response.
type Stream struct {
	chunks   chan string
	response string
	err      error
	done     chan struct{}
	mu       sync.RWMutex
}

// Chunks returns the channel of response chunks.
func (s *Stream) Chunks() <-chan string {
	return s.chunks
}

// Response returns the complete response after streaming is done.
func (s *Stream) Response() string {
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.response
}

// Err returns any error that occurred during streaming.
func (s *Stream) Err() error {
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}
