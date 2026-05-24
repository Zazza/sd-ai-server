package process

import (
	"strings"
	"sync"
)

const RingBufferSize = 100

type RingBuffer struct {
	mu    sync.RWMutex
	lines []string
	size  int
	head  int
	count int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		lines: make([]string, size),
		size:  size,
	}
}

func (r *RingBuffer) Write(line string) {
	r.mu.Lock()
	r.lines[r.head] = line
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
	r.mu.Unlock()
}

func (r *RingBuffer) Lines(n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n > r.count {
		n = r.count
	}
	result := make([]string, 0, n)
	start := r.head - n
	if start < 0 {
		start += r.size
	}
	for i := 0; i < n; i++ {
		idx := (start + i) % r.size
		result = append(result, r.lines[idx])
	}
	return result
}

type LogCapture struct {
	buf *RingBuffer
}

func NewLogCapture(size int) *LogCapture {
	return &LogCapture{buf: NewRingBuffer(size)}
}

func (lc *LogCapture) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			lc.buf.Write(line)
		}
	}
	return len(p), nil
}

func (lc *LogCapture) Lines(n int) []string {
	return lc.buf.Lines(n)
}
