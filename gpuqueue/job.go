package gpuqueue

import (
	"errors"
	"strings"
	"time"
)

type Kind string

const (
	KindSD  Kind = "sd"
	KindLLM Kind = "llm"
	KindYue Kind = "yue"
)

func (k Kind) Valid() bool {
	switch k {
	case KindSD, KindLLM, KindYue:
		return true
	}
	return false
}

type Spec struct {
	Kind      Kind
	Client    string
	WeightMB  int
	Priority  int
	AutoRenew bool
}

type Job struct {
	ID            string    `json:"id"`
	Kind          Kind      `json:"kind"`
	Client        string    `json:"client"`
	WeightMB      int       `json:"weight_mb"`
	Priority      int       `json:"priority"`
	SubmittedAt   time.Time `json:"submitted_at"`
	StartedAt     time.Time `json:"started_at"`
	LeaseDeadline time.Time `json:"lease_deadline"`
	Position      int       `json:"position"`
}

type Config struct {
	TotalBudgetMB int
	MaxWait       time.Duration
	LeaseTTL      time.Duration
	MaxWarnings   int
	MaxQueueLen   int
}

type Status struct {
	Enabled  bool     `json:"enabled"`
	Budget   int      `json:"budget"`
	Running  []Job    `json:"running"`
	Queue    []Job    `json:"queue"`
	Warnings []string `json:"warnings"`
}

type Lease struct {
	q  *Queue
	id string
}

func (l *Lease) ID() string {
	return l.id
}

func (l *Lease) Release() {
	if l == nil || l.q == nil {
		return
	}
	l.q.Release(l.id)
}

var (
	ErrTimeout   = errors.New("gpu queue timeout")
	ErrCancelled = errors.New("gpu queue cancelled")
	ErrNotFound  = errors.New("gpu lease not found")
	ErrQueueFull = errors.New("gpu queue full")
)

func SanitizeClient(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
