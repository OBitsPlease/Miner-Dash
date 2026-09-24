package agent

import (
	"bytes"
	"io"
)

func bytesNewReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}
