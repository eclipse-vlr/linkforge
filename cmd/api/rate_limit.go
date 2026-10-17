package main

import (
	"container/list"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

const (
	createLinkLimit     = 10
	createLinkWindow    = time.Minute
	maxRateLimitClients = 10_000
)

type clientRequests struct {
	ip    netip.Addr
	times [createLinkLimit]time.Time
	count int
}

type createLinkLimiter struct {
	mu      sync.Mutex
	clients map[netip.Addr]*list.Element
	// Oldest last-admitted request first, for expiry without scanning the map.
	activity   list.List
	now        func() time.Time
	maxClients int
}

func newCreateLinkLimiter() *createLinkLimiter {
	return &createLinkLimiter{
		clients:    make(map[netip.Addr]*list.Element),
		now:        time.Now,
		maxClients: maxRateLimitClients,
	}
}

func (l *createLinkLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "invalid client address", http.StatusBadRequest)
			return
		}
		// Forwarded headers are untrusted. Canonicalize IPv4-mapped IPv6 too.
		ip := peer.Addr().Unmap().WithZone("")
		status, retry := l.admit(ip)
		if status != 0 {
			seconds := (retry + time.Second - 1) / time.Second
			w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
			http.Error(w, http.StatusText(status), status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Admission counts every POST attempt, including requests later rejected by the
// handler. The mutex makes checking the quota and recording admission atomic.
func (l *createLinkLimiter) admit(ip netip.Addr) (status int, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-createLinkWindow)
	for front := l.activity.Front(); front != nil; front = l.activity.Front() {
		client := front.Value.(*clientRequests)
		if client.times[client.count-1].After(cutoff) {
			break
		}
		delete(l.clients, client.ip)
		l.activity.Remove(front)
	}

	element := l.clients[ip]
	if element == nil {
		if len(l.clients) >= l.maxClients {
			oldest := l.activity.Front().Value.(*clientRequests)
			return http.StatusServiceUnavailable, oldest.times[oldest.count-1].Add(createLinkWindow).Sub(now)
		}
		element = l.activity.PushBack(&clientRequests{ip: ip})
		l.clients[ip] = element
	}
	client := element.Value.(*clientRequests)
	expired := 0
	for expired < client.count && !client.times[expired].After(cutoff) {
		expired++
	}
	client.count = copy(client.times[:], client.times[expired:client.count])
	if client.count == createLinkLimit {
		return http.StatusTooManyRequests, client.times[0].Add(createLinkWindow).Sub(now)
	}
	client.times[client.count] = now
	client.count++
	l.activity.MoveToBack(element)
	return 0, 0
}
