package client

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

const (
	contentGlobalConcurrency = 20
	contentKeyConcurrency    = 8
	contentPairConcurrency   = 4
	contentKeyRate           = 30.0
	contentKeyBurst          = 60.0
	contentPairRate          = 15.0
	contentPairBurst         = 30.0
	maxProtectionBuckets     = 8192
	maxPairBucketsPerKey     = 256
)

type rateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
	owner    string
}

type Protection struct {
	mu          sync.Mutex
	now         func() time.Time
	global      int
	buckets     map[string]rateBucket
	keyActive   map[string]int
	pairActive  map[string]int
	pairBuckets map[string]int
}

func NewProtection() *Protection {
	return &Protection{now: time.Now, buckets: make(map[string]rateBucket), keyActive: make(map[string]int), pairActive: make(map[string]int), pairBuckets: make(map[string]int)}
}

func (p *Protection) Global(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Allow", http.MethodGet)
			httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindMethodNotAllowed, Code: "method_not_allowed", Message: "请求方法不受支持"})
			return
		}
		p.mu.Lock()
		if p.global >= contentGlobalConcurrency {
			p.mu.Unlock()
			w.Header().Set("Retry-After", "1")
			httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnavailable, Code: "content_api_busy", Message: "内容 API 暂时繁忙"})
			return
		}
		p.global++
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			p.global--
			p.mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

func (p *Protection) Acquire(keyID, ip string) (func(), time.Duration, error) {
	now := p.now()
	pair := keyID + "\x00" + ip
	keyBucketID, pairBucketID := "key\x00"+keyID, "pair\x00"+pair
	p.mu.Lock()
	defer p.mu.Unlock()
	_, pairExists := p.buckets[pairBucketID]
	if !pairExists {
		if p.pairBuckets[keyID] >= maxPairBucketsPerKey {
			p.cleanupStale(now)
		}
		if p.pairBuckets[keyID] >= maxPairBucketsPerKey {
			return nil, time.Second, &apperror.Error{Kind: apperror.KindTooManyRequests, Code: "content_rate_limited", Message: "内容 API 请求过于频繁"}
		}
	}
	if !p.ensureCapacity(now, keyBucketID, pairBucketID) {
		return nil, time.Second, &apperror.Error{Kind: apperror.KindUnavailable, Code: "content_api_busy", Message: "内容 API 暂时繁忙"}
	}
	_, pairExists = p.buckets[pairBucketID]
	keyBucket := p.refill(keyBucketID, now, contentKeyRate, contentKeyBurst)
	pairBucket := p.refill(pairBucketID, now, contentPairRate, contentPairBurst)
	if keyBucket.tokens < 1 || pairBucket.tokens < 1 {
		p.saveBuckets(keyBucketID, pairBucketID, keyID, keyBucket, pairBucket, pairExists)
		retry := tokenRetry(keyBucket.tokens, contentKeyRate)
		if pairRetry := tokenRetry(pairBucket.tokens, contentPairRate); pairRetry > retry {
			retry = pairRetry
		}
		return nil, retry, &apperror.Error{Kind: apperror.KindTooManyRequests, Code: "content_rate_limited", Message: "内容 API 请求过于频繁"}
	}
	if p.keyActive[keyID] >= contentKeyConcurrency || p.pairActive[pair] >= contentPairConcurrency {
		p.saveBuckets(keyBucketID, pairBucketID, keyID, keyBucket, pairBucket, pairExists)
		return nil, time.Second, &apperror.Error{Kind: apperror.KindTooManyRequests, Code: "content_concurrency_limited", Message: "内容 API 并发请求过多"}
	}
	keyBucket.tokens--
	pairBucket.tokens--
	p.saveBuckets(keyBucketID, pairBucketID, keyID, keyBucket, pairBucket, pairExists)
	p.keyActive[keyID]++
	p.pairActive[pair]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if p.keyActive[keyID]--; p.keyActive[keyID] == 0 {
				delete(p.keyActive, keyID)
			}
			if p.pairActive[pair]--; p.pairActive[pair] == 0 {
				delete(p.pairActive, pair)
			}
			p.mu.Unlock()
		})
	}, 0, nil
}

func (p *Protection) refill(key string, now time.Time, rate, burst float64) rateBucket {
	bucket, exists := p.buckets[key]
	if !exists {
		return rateBucket{tokens: burst, updated: now, lastSeen: now}
	}
	bucket.tokens = math.Min(burst, bucket.tokens+now.Sub(bucket.updated).Seconds()*rate)
	bucket.updated, bucket.lastSeen = now, now
	return bucket
}

func (p *Protection) saveBuckets(keyBucketID, pairBucketID, keyID string, keyBucket, pairBucket rateBucket, pairExists bool) {
	p.buckets[keyBucketID] = keyBucket
	pairBucket.owner = keyID
	p.buckets[pairBucketID] = pairBucket
	if !pairExists {
		p.pairBuckets[keyID]++
	}
}

func (p *Protection) ensureCapacity(now time.Time, keys ...string) bool {
	needed := 0
	for _, key := range keys {
		if _, exists := p.buckets[key]; !exists {
			needed++
		}
	}
	if len(p.buckets)+needed <= maxProtectionBuckets {
		return true
	}
	p.cleanupStale(now)
	needed = 0
	for _, key := range keys {
		if _, exists := p.buckets[key]; !exists {
			needed++
		}
	}
	return len(p.buckets)+needed <= maxProtectionBuckets
}

func (p *Protection) cleanupStale(now time.Time) {
	cutoff := now.Add(-10 * time.Minute)
	for key, bucket := range p.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(p.buckets, key)
			if bucket.owner != "" {
				if p.pairBuckets[bucket.owner]--; p.pairBuckets[bucket.owner] == 0 {
					delete(p.pairBuckets, bucket.owner)
				}
			}
		}
	}
}

func tokenRetry(tokens, rate float64) time.Duration {
	if tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - tokens) / rate * float64(time.Second)))
}

func WriteProtectionError(w http.ResponseWriter, r *http.Request, retry time.Duration, err error) {
	seconds := int(math.Ceil(retry.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprint(seconds))
	httpx.WriteError(w, r, err)
}
