package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime        = 3
	argonMemory      = 64 * 1024
	argonThreads     = 2
	argonKeyLen      = 32
	argonConcurrency = 4
)

var argonSlots = make(chan struct{}, argonConcurrency)

func HashPassword(password string, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	if password == "" {
		return "", errors.New("密码不能为空")
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("生成密码盐值: %w", err)
	}
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	valid, _ := verifyPasswordContext(context.Background(), encoded, password)
	return valid
}

func verifyPasswordContext(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, nil
	}
	var memory uint64
	var times uint64
	var threads uint64
	for _, value := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(value, "=", 2)
		if len(pair) != 2 {
			return false, nil
		}
		number, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false, nil
		}
		switch pair[0] {
		case "m":
			memory = number
		case "t":
			times = number
		case "p":
			threads = number
		default:
			return false, nil
		}
	}
	if memory == 0 || memory > 1024*1024 || times == 0 || times > 10 || threads == 0 || threads > 16 {
		return false, nil
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 {
		return false, nil
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, nil
	}
	select {
	case argonSlots <- struct{}{}:
		defer func() { <-argonSlots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	got := argon2.IDKey([]byte(password), salt, uint32(times), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
