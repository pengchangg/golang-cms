package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMemoryStoreStreamingLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.Now = func() time.Time { return now }
	body := "streamed object"
	digest := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(digest[:])
	metadata, err := store.Put(context.Background(), PutObjectRequest{ObjectKey: "assets/ast_0123456789abcdef0123456789abcdef/0123456789abcdef0123456789abcdef", ContentType: "image/png", Size: int64(len(body)), SHA256: hash}, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SHA256 != hash || metadata.ETag == "" {
		t.Fatalf("元数据不正确: %+v", metadata)
	}
	reader, got, err := store.Get(context.Background(), metadata.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != body || got != metadata {
		t.Fatalf("流式读取不一致: %q %+v", data, got)
	}
	if err := store.Delete(context.Background(), metadata.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Head(context.Background(), metadata.ObjectKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("期望对象不存在，得到 %v", err)
	}
}

func TestMemoryStoreRejectsMismatchExpiryAndFailure(t *testing.T) {
	now := time.Now().UTC()
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.Now = func() time.Time { return now }
	if _, err := store.Put(context.Background(), PutObjectRequest{ObjectKey: "key", ContentType: "text/plain", Size: 2, SHA256: strings.Repeat("0", 64)}, strings.NewReader("abc")); !errors.Is(err, ErrStoreConfig) {
		t.Fatalf("期望大小不匹配，得到 %v", err)
	}
	if _, err := store.SignPut(context.Background(), SignPutRequest{ExpiresAt: now.Add(-time.Second)}); !errors.Is(err, ErrStoreConfig) {
		t.Fatalf("期望过期签名失败，得到 %v", err)
	}
	if _, err := store.SignGet(context.Background(), SignGetRequest{ExpiresAt: now.Add(6 * time.Minute)}); !errors.Is(err, ErrStoreConfig) {
		t.Fatalf("期望超限签名失败，得到 %v", err)
	}
	store.Failure = ErrStoreUnavailable
	if _, err := store.Head(context.Background(), "key"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("期望临时故障，得到 %v", err)
	}
}
