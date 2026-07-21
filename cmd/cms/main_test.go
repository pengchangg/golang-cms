package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cms/internal/auth"
	"cms/internal/platform/config"
)

func TestExecuteRejectsUnknownCommand(t *testing.T) {
	code, err := execute([]string{"unknown"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code != 2 || err == nil {
		t.Fatalf("execute() = (%d, %v)", code, err)
	}
}

func TestExecuteRejectsUnknownAdminCommand(t *testing.T) {
	code, err := execute([]string{"admin", "unknown", "admin", "管理员"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code != 2 || err == nil || !strings.Contains(err.Error(), "admin <ensure|reset-password>") {
		t.Fatalf("execute() = (%d, %v)", code, err)
	}
}

func TestRunParallelCancelsPeerOnFatalError(t *testing.T) {
	fatal := errors.New("fatal")
	peerStopped := make(chan struct{})
	err := runParallel(context.Background(), func() error { return nil },
		func(context.Context) error { return fatal },
		func(ctx context.Context) error { <-ctx.Done(); close(peerStopped); return ctx.Err() },
	)
	if !errors.Is(err, fatal) {
		t.Fatalf("runParallel() error = %v", err)
	}
	select {
	case <-peerStopped:
	default:
		t.Fatal("peer was not canceled")
	}
}

func TestRunParallelGracefullyStopsAllServices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var stopped atomic.Int32
	service := func(ctx context.Context) error { <-ctx.Done(); stopped.Add(1); return nil }
	cancel()
	if err := runParallel(ctx, func() error { return nil }, service, service); err != nil {
		t.Fatal(err)
	}
	if stopped.Load() != 2 {
		t.Fatalf("stopped services = %d", stopped.Load())
	}
}

func TestRunParallelReturnsShutdownError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	want := errors.New("shutdown failed")
	err := runParallel(ctx, func() error { return want }, func(ctx context.Context) error { <-ctx.Done(); return nil })
	if !errors.Is(err, want) {
		t.Fatalf("runParallel() error = %v", err)
	}
}

func TestRunParallelHardStopsWithoutWaitingForStuckService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	defer close(release)
	cancel()
	defer func() {
		recovered, ok := recover().(error)
		if !ok || !errors.Is(recovered, errParallelShutdownTimeout) {
			t.Fatalf("硬终止参数 = %v", recovered)
		}
	}()
	runParallelWithTimeout(ctx, func() error { return nil }, 10*time.Millisecond, func(err error) { panic(err) }, func(context.Context) error {
		<-release
		return nil
	})
	t.Fatal("整体关闭超时后函数不应返回")
}

func TestExecuteRejectsMissingCommand(t *testing.T) {
	code, err := execute(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code != 2 || err == nil {
		t.Fatalf("execute() = (%d, %v)", code, err)
	}
}

func TestBuildSMSProviderSelectsConfiguredAdapter(t *testing.T) {
	fixed, err := buildSMSProvider(config.Config{SMSProvider: "fixed", SMSFixedCode: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if provider, ok := fixed.(auth.FixedSMSProvider); !ok || provider.Code != "123456" {
		t.Fatalf("fixed provider = %#v", fixed)
	}
	tencent, err := buildSMSProvider(config.Config{SMSProvider: "tencent", TencentSecretID: "id", TencentSecretKey: "key", TencentSMSRegion: "ap-guangzhou", TencentSMSSDKAppID: "app", TencentSMSSignName: "sign", TencentSMSTemplateID: "template"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tencent.(*auth.TencentSMSProvider); !ok {
		t.Fatalf("tencent provider = %#v", tencent)
	}
}
