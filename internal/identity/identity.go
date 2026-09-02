package identity

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

func Hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum)
}
