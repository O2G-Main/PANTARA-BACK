package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

// SecurityStore implements port.SecurityStore using Redis with automatic in-memory fallback
// so a transient Redis outage never causes false 403/503 responses.
type SecurityStore struct {
	client *goredis.Client
	mem    *memoryFallback
}

func NewSecurityStore(redisURL string) *SecurityStore {
	s := &SecurityStore{mem: newMemoryFallback()}
	if redisURL == "" {
		return s
	}
	opt, err := goredis.ParseURL(redisURL)
	if err != nil {
		return s
	}
	opt.DialTimeout = 2 * time.Second
	opt.ReadTimeout = 2 * time.Second
	opt.WriteTimeout = 2 * time.Second
	opt.PoolSize = 100
	opt.MinIdleConns = 10
	s.client = goredis.NewClient(opt)
	return s
}

func (s *SecurityStore) Ping(ctx context.Context) error {
	if s.client == nil {
		return nil
	}
	return s.client.Ping(ctx).Err()
}

// Close closes the underlying Redis connection pool if initialized.
func (s *SecurityStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *SecurityStore) AllowRate(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if s.client != nil {
		rKey := "rl:" + key
		pipe := s.client.TxPipeline()
		incr := pipe.Incr(ctx, rKey)
		pipe.Expire(ctx, rKey, window)
		if _, err := pipe.Exec(ctx); err == nil {
			return int(incr.Val()) <= limit, nil
		}
	}
	return s.mem.allowRate(key, limit, window), nil
}

func (s *SecurityStore) GetLoginLock(ctx context.Context, key string) (bool, time.Duration, error) {
	if s.client != nil {
		ttl, err := s.client.TTL(ctx, "lock:"+key).Result()
		if err == nil && ttl > 0 {
			return true, ttl, nil
		}
		if err == nil {
			return false, 0, nil
		}
	}
	locked, rem := s.mem.getLock(key)
	return locked, rem, nil
}

func (s *SecurityStore) RecordLoginFailure(ctx context.Context, key string, maxTries int, lockTTL time.Duration) (int, time.Time, error) {
	if s.client != nil {
		failKey := "fail:" + key
		count, err := s.client.Incr(ctx, failKey).Result()
		if err == nil {
			_ = s.client.Expire(ctx, failKey, lockTTL).Err()
			if int(count) >= maxTries {
				_ = s.client.Set(ctx, "lock:"+key, "1", lockTTL).Err()
				return int(count), time.Now().UTC().Add(lockTTL), nil
			}
			return int(count), time.Time{}, nil
		}
	}
	count, until := s.mem.recordFailure(key, maxTries, lockTTL)
	return count, until, nil
}

func (s *SecurityStore) ClearLoginFailures(ctx context.Context, key string) error {
	if s.client != nil {
		_ = s.client.Del(ctx, "fail:"+key, "lock:"+key).Err()
	}
	s.mem.clearFailures(key)
	return nil
}

func (s *SecurityStore) GetSessionVersion(ctx context.Context, userID string) (int64, bool, error) {
	if s.client != nil {
		val, err := s.client.Get(ctx, "sv:"+userID).Result()
		if err == nil {
			if n, pErr := strconv.ParseInt(val, 10, 64); pErr == nil {
				return n, true, nil
			}
		}
	}
	v, ok := s.mem.getSessionVersion(userID)
	return v, ok, nil
}

func (s *SecurityStore) SetSessionVersion(ctx context.Context, userID string, version int64, ttl time.Duration) error {
	if s.client != nil {
		_ = s.client.Set(ctx, "sv:"+userID, fmt.Sprintf("%d", version), ttl).Err()
	}
	s.mem.setSessionVersion(userID, version, ttl)
	return nil
}

func (s *SecurityStore) GetCachedDNA(ctx context.Context, hash string) (*task.TaskDNA, bool, error) {
	if s.client != nil {
		raw, err := s.client.Get(ctx, "dna:"+hash).Bytes()
		if err == nil {
			var dna task.TaskDNA
			if jErr := json.Unmarshal(raw, &dna); jErr == nil {
				return &dna, true, nil
			}
		}
	}
	dna, ok := s.mem.getDNA(hash)
	return dna, ok, nil
}

func (s *SecurityStore) SetCachedDNA(ctx context.Context, hash string, dna task.TaskDNA, ttl time.Duration) error {
	if s.client != nil {
		if raw, err := json.Marshal(dna); err == nil {
			_ = s.client.Set(ctx, "dna:"+hash, raw, ttl).Err()
		}
	}
	s.mem.setDNA(hash, dna, ttl)
	return nil
}

type rateEntry struct {
	count int
	until time.Time
}

type failEntry struct {
	count     int
	lockedTil time.Time
	expiresAt time.Time
}

type svEntry struct {
	ver   int64
	until time.Time
}

type dnaEntry struct {
	dna   task.TaskDNA
	until time.Time
}

type memoryFallback struct {
	mu    sync.Mutex
	rates map[string]rateEntry
	fails map[string]failEntry
	svs   map[string]svEntry
	dnas  map[string]dnaEntry
}

func newMemoryFallback() *memoryFallback {
	return &memoryFallback{
		rates: make(map[string]rateEntry),
		fails: make(map[string]failEntry),
		svs:   make(map[string]svEntry),
		dnas:  make(map[string]dnaEntry),
	}
}

func (m *memoryFallback) allowRate(key string, limit int, window time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	cur, ok := m.rates[key]
	if !ok || now.After(cur.until) {
		m.rates[key] = rateEntry{count: 1, until: now.Add(window)}
		return true
	}
	cur.count++
	m.rates[key] = cur
	return cur.count <= limit
}

func (m *memoryFallback) getLock(key string) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.fails[key]
	if !ok {
		return false, 0
	}
	now := time.Now().UTC()
	if now.Before(cur.lockedTil) {
		return true, cur.lockedTil.Sub(now)
	}
	return false, 0
}

func (m *memoryFallback) recordFailure(key string, maxTries int, lockTTL time.Duration) (int, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	cur, ok := m.fails[key]
	if !ok || now.After(cur.expiresAt) {
		cur = failEntry{expiresAt: now.Add(lockTTL)}
	}
	cur.count++
	cur.expiresAt = now.Add(lockTTL)
	if cur.count >= maxTries {
		cur.lockedTil = now.Add(lockTTL)
	}
	m.fails[key] = cur
	return cur.count, cur.lockedTil
}

func (m *memoryFallback) clearFailures(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.fails, key)
}

func (m *memoryFallback) getSessionVersion(userID string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.svs[userID]
	if !ok || time.Now().UTC().After(cur.until) {
		return 0, false
	}
	return cur.ver, true
}

func (m *memoryFallback) setSessionVersion(userID string, v int64, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.svs[userID] = svEntry{ver: v, until: time.Now().UTC().Add(ttl)}
}

func (m *memoryFallback) getDNA(hash string) (*task.TaskDNA, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dnas[hash]
	if !ok || time.Now().UTC().After(cur.until) {
		return nil, false
	}
	cp := cur.dna
	return &cp, true
}

func (m *memoryFallback) setDNA(hash string, dna task.TaskDNA, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dnas[hash] = dnaEntry{dna: dna, until: time.Now().UTC().Add(ttl)}
}
