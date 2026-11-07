package access

import "os"

func writeRaw(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
