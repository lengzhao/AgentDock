package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/lengzhao/AgentDock/config"
)

// VerifyMarkerFile returns the path where the running bot records a handled
// verification token.
func VerifyMarkerFile(p config.Paths) string {
	return p.StateDir + "/verify-ok"
}

// ReadVerifyMarker reports whether the marker contains nonce.
func ReadVerifyMarker(path, nonce string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read verify marker: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == nonce {
			return true, nil
		}
	}
	return false, nil
}

// WriteVerifyMarker records that the bot handled nonce.
func WriteVerifyMarker(p config.Paths, nonce string) error {
	return config.WriteFileAtomic(VerifyMarkerFile(p), []byte(nonce+"\n"))
}
