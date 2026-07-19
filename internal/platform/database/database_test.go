package database

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestRequireSecureTransportRejectsRemotePlaintext(t *testing.T) {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = "mysql.internal:3306"
	if err := requireSecureTransport(cfg, false); err == nil {
		t.Fatal("requireSecureTransport() expected an error")
	}
}

func TestRequireSecureTransportAllowsLoopbackAndExplicitDevelopmentOverride(t *testing.T) {
	for _, test := range []struct {
		name          string
		addr          string
		allowInsecure bool
	}{
		{name: "loopback", addr: "127.0.0.1:3306"},
		{name: "localhost", addr: "localhost:3306"},
		{name: "explicit override", addr: "mysql:3306", allowInsecure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := mysql.NewConfig()
			cfg.Net = "tcp"
			cfg.Addr = test.addr
			if err := requireSecureTransport(cfg, test.allowInsecure); err != nil {
				t.Fatalf("requireSecureTransport() error = %v", err)
			}
		})
	}
}
