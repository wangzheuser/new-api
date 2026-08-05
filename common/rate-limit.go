package common

import (
	"sync"
	"time"
)

type InMemoryRateLimiter struct {
	store              map[string]*[]int64
	mutex              sync.Mutex
	expirationDuration time.Duration
}

// pruneExpiredRequests removes timestamps outside the active window.
func (l *InMemoryRateLimiter) pruneExpiredRequests(key string, now, duration int64) []int64 {
	queue, ok := l.store[key]
	if !ok {
		return nil
	}
	requests := *queue
	firstActive := 0
	for firstActive < len(requests) && now-requests[firstActive] >= duration {
		firstActive++
	}
	if firstActive > 0 {
		requests = append([]int64(nil), requests[firstActive:]...)
		*queue = requests
	}
	return requests
}

// Available reports whether a request can be recorded without consuming capacity.
func (l *InMemoryRateLimiter) Available(key string, maxRequestNum int, duration int64) bool {
	if maxRequestNum <= 0 {
		return true
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	requests := l.pruneExpiredRequests(key, time.Now().Unix(), duration)
	return len(requests) < maxRequestNum
}

func (l *InMemoryRateLimiter) Init(expirationDuration time.Duration) {
	if l.store == nil {
		l.mutex.Lock()
		if l.store == nil {
			l.store = make(map[string]*[]int64)
			l.expirationDuration = expirationDuration
			if expirationDuration > 0 {
				go l.clearExpiredItems()
			}
		}
		l.mutex.Unlock()
	}
}

func (l *InMemoryRateLimiter) clearExpiredItems() {
	for {
		time.Sleep(l.expirationDuration)
		l.mutex.Lock()
		now := time.Now().Unix()
		for key := range l.store {
			queue := l.store[key]
			size := len(*queue)
			if size == 0 || now-(*queue)[size-1] > int64(l.expirationDuration.Seconds()) {
				delete(l.store, key)
			}
		}
		l.mutex.Unlock()
	}
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	if maxRequestNum <= 0 {
		return true
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := time.Now().Unix()
	requests := l.pruneExpiredRequests(key, now, duration)
	if len(requests) >= maxRequestNum {
		return false
	}
	queue, ok := l.store[key]
	if !ok {
		s := make([]int64, 0, maxRequestNum)
		l.store[key] = &s
		queue = &s
	}
	*queue = append(*queue, now)
	return true
}
