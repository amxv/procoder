package exchange

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	TaskRefNamespace = "refs/heads/procoder"
)

var exchangeIDPattern = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`)

func NewID() (string, error) {
	return GenerateID(time.Now().UTC(), rand.Reader)
}

func GenerateID(now time.Time, random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("random source is nil")
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	stamp := now.UTC().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", stamp, hex.EncodeToString(buf)), nil
}

func TaskRootRef(exchangeID string) string {
	normalized, ok := normalizeExchangeID(exchangeID)
	if !ok {
		return ""
	}
	return TaskRefNamespace + "/" + normalized
}

func TaskRefPrefix(exchangeID string) string {
	return TaskRootRef(exchangeID)
}

func IsTaskRef(exchangeID, ref string) bool {
	root := TaskRootRef(exchangeID)
	if root == "" {
		return false
	}
	return ref == root || strings.HasPrefix(ref, root+"/")
}

func normalizeExchangeID(exchangeID string) (string, bool) {
	exchangeID = strings.TrimSpace(exchangeID)
	if !exchangeIDPattern.MatchString(exchangeID) {
		return "", false
	}
	return exchangeID, true
}
