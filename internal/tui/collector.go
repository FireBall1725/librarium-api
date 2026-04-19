// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package tui

import (
	"sync"
	"time"
)

const sparklineWindow = 60 // seconds of history

// RequestSample represents a single HTTP request's metrics.
type RequestSample struct {
	Time       time.Time
	Method     string
	Path       string
	RemoteAddr string // real client IP (no port)
	Client     string // classified client type: "ios", "web", "api", etc.
	ErrorMsg   string // non-empty for 5xx: the real Go error from the handler
	Status     int
	Duration   time.Duration
}

// LogEntry represents a single log event.
type LogEntry struct {
	Time    time.Time
	Level   string // "DEBUG", "INFO", "WARN", "ERROR"
	Message string
	Attrs   map[string]string
}

// QueueStats holds a point-in-time snapshot of the import job queue.
type QueueStats struct {
	Pending    int
	Processing int
	Done       int
	Failed     int
	Active     []ActiveJobInfo // pending + processing jobs, newest first
}

// ActiveJobInfo holds display data for a single pending or running import job.
type ActiveJobInfo struct {
	ID            string // first 8 chars of the UUID
	LibraryName   string
	Status        string
	TotalRows     int
	ProcessedRows int
	FailedRows    int
	SkippedRows   int
	UpdatedAt     time.Time
}

// Collector gathers request metrics and log entries for the TUI.
// It is safe for concurrent use.
type Collector struct {
	mu          sync.Mutex
	activeConns int64 // current open HTTP connections

	// reqBuckets holds per-second request counts for the last sparklineWindow seconds.
	reqBuckets [sparklineWindow]int
	lastBucket int64 // unix second of the last bucket written

	// logBuf holds the most recent log entries (ring buffer).
	logBuf [500]LogEntry
	logPos int
	logLen int

	// queueStats is the latest snapshot pushed by the queue poller.
	queueStats QueueStats

	// reqCh and logCh deliver new samples to the TUI event loop.
	reqCh chan RequestSample
	logCh chan LogEntry
}

func NewCollector() *Collector {
	return &Collector{
		reqCh: make(chan RequestSample, 256),
		logCh: make(chan LogEntry, 256),
	}
}

// RecordRequest records an HTTP request. Satisfies api.MetricsCollector and
// middleware.MetricsRecorder interfaces.
func (c *Collector) RecordRequest(method, path, remoteAddr, client, errMsg string, status int, duration time.Duration) {
	now := time.Now()
	bucket := now.Unix()

	c.mu.Lock()
	if c.lastBucket == 0 {
		c.lastBucket = bucket
	}
	// Advance past any empty buckets
	for c.lastBucket < bucket {
		c.lastBucket++
		c.reqBuckets[c.lastBucket%sparklineWindow] = 0
	}
	c.reqBuckets[bucket%sparklineWindow]++
	c.mu.Unlock()

	select {
	case c.reqCh <- RequestSample{Time: now, Method: method, Path: path, RemoteAddr: remoteAddr, Client: client, ErrorMsg: errMsg, Status: status, Duration: duration}:
	default:
	}
}

// ReqSparkline returns the full sparklineWindow per-second counts (newest last).
func (c *Collector) ReqSparkline() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	// advance stale buckets
	for c.lastBucket < now {
		c.lastBucket++
		c.reqBuckets[c.lastBucket%sparklineWindow] = 0
	}

	out := make([]int, sparklineWindow)
	for i := 0; i < sparklineWindow; i++ {
		sec := now - int64(sparklineWindow-1-i)
		out[i] = c.reqBuckets[sec%sparklineWindow]
	}
	return out
}

// AddLog records a log entry.
func (c *Collector) AddLog(entry LogEntry) {
	c.mu.Lock()
	c.logBuf[c.logPos%len(c.logBuf)] = entry
	c.logPos++
	if c.logLen < len(c.logBuf) {
		c.logLen++
	}
	c.mu.Unlock()

	select {
	case c.logCh <- entry:
	default:
	}
}

// TrackConn increments or decrements the active connection counter.
// delta should be +1 when a connection opens and -1 when it closes.
func (c *Collector) TrackConn(delta int64) {
	c.mu.Lock()
	c.activeConns += delta
	if c.activeConns < 0 {
		c.activeConns = 0
	}
	c.mu.Unlock()
}

// ActiveConns returns the current number of open HTTP connections.
func (c *Collector) ActiveConns() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeConns
}

// UpdateQueueStats replaces the queue stats snapshot. Called by the queue poller goroutine.
func (c *Collector) UpdateQueueStats(s QueueStats) {
	c.mu.Lock()
	c.queueStats = s
	c.mu.Unlock()
}

// GetQueueStats returns the latest queue stats snapshot.
func (c *Collector) GetQueueStats() QueueStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queueStats
}

// ReqCh returns the channel that delivers new request samples.
func (c *Collector) ReqCh() <-chan RequestSample { return c.reqCh }

// LogCh returns the channel that delivers new log entries.
func (c *Collector) LogCh() <-chan LogEntry { return c.logCh }
