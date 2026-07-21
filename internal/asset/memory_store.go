package asset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"sync"
	"time"
)

// MemoryStore 是不依赖真实对象存储的并发安全 Adapter，供领域测试使用。
type MemoryStore struct {
	mu             sync.RWMutex
	objects        map[string]memoryObject
	Now            func() time.Time
	UploadMaxTTL   time.Duration
	DownloadMaxTTL time.Duration
	Failure        error
}

type memoryObject struct {
	data     []byte
	metadata ObjectMetadata
}

func NewMemoryStore(uploadMaxTTL, downloadMaxTTL time.Duration) *MemoryStore {
	return &MemoryStore{objects: map[string]memoryObject{}, Now: func() time.Time { return time.Now().UTC() }, UploadMaxTTL: uploadMaxTTL, DownloadMaxTTL: downloadMaxTTL}
}

func (s *MemoryStore) SignPut(_ context.Context, input SignPutRequest) (SignedRequest, error) {
	if s.Failure != nil {
		return SignedRequest{}, s.Failure
	}
	if err := s.validExpiry(input.ExpiresAt, s.UploadMaxTTL); err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{Method: "PUT", URL: memoryURL(input.ObjectKey, input.ExpiresAt), Headers: map[string]string{"Content-Type": input.ContentType, "If-None-Match": "*", "x-amz-meta-sha256": input.SHA256}, ExpiresAt: input.ExpiresAt.UTC()}, nil
}

func (s *MemoryStore) SignGet(_ context.Context, input SignGetRequest) (SignedRequest, error) {
	if s.Failure != nil {
		return SignedRequest{}, s.Failure
	}
	if err := s.validExpiry(input.ExpiresAt, s.DownloadMaxTTL); err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{Method: "GET", URL: memoryURL(input.ObjectKey, input.ExpiresAt), Headers: map[string]string{}, ExpiresAt: input.ExpiresAt.UTC()}, nil
}

func (s *MemoryStore) Head(_ context.Context, key string) (ObjectMetadata, error) {
	if s.Failure != nil {
		return ObjectMetadata{}, s.Failure
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.objects[key]
	if !ok {
		return ObjectMetadata{}, ErrObjectNotFound
	}
	return value.metadata, nil
}

func (s *MemoryStore) Put(_ context.Context, input PutObjectRequest, body io.Reader) (ObjectMetadata, error) {
	if s.Failure != nil {
		return ObjectMetadata{}, s.Failure
	}
	data, err := io.ReadAll(io.LimitReader(body, input.Size+1))
	if err != nil {
		return ObjectMetadata{}, ErrStoreUnavailable
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != input.Size || hex.EncodeToString(digest[:]) != input.SHA256 {
		return ObjectMetadata{}, ErrStoreConfig
	}
	now := s.Now().UTC()
	metadata := ObjectMetadata{ObjectKey: input.ObjectKey, Size: input.Size, ContentType: input.ContentType, SHA256: input.SHA256, ETag: hex.EncodeToString(digest[:16]), LastModified: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[input.ObjectKey]; exists {
		return ObjectMetadata{}, ErrStoreConfig
	}
	s.objects[input.ObjectKey] = memoryObject{data: append([]byte(nil), data...), metadata: metadata}
	return metadata, nil
}

func (s *MemoryStore) Get(_ context.Context, key string) (io.ReadCloser, ObjectMetadata, error) {
	if s.Failure != nil {
		return nil, ObjectMetadata{}, s.Failure
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.objects[key]
	if !ok {
		return nil, ObjectMetadata{}, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), value.data...))), value.metadata, nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	if s.Failure != nil {
		return s.Failure
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return ErrObjectNotFound
	}
	delete(s.objects, key)
	return nil
}

func (s *MemoryStore) validExpiry(expiresAt time.Time, max time.Duration) error {
	ttl := expiresAt.Sub(s.Now())
	if ttl <= 0 || ttl > max {
		return ErrStoreConfig
	}
	return nil
}

func memoryURL(key string, expiresAt time.Time) string {
	u := url.URL{Scheme: "https", Host: "memory.invalid", Path: "/" + key}
	query := u.Query()
	query.Set("expires", expiresAt.UTC().Format(time.RFC3339Nano))
	u.RawQuery = query.Encode()
	return u.String()
}
