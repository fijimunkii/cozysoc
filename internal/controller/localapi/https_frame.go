package localapi

import (
	"bufio"
	"encoding/json"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"net"
)

// Review/result includes an exact request and privacy disclosure. A long target
// can expand sixfold under JSON HTML escaping and appears twice. Requests and
// decisions retain the existing 8 KiB ingress limit; only HTTPS responses use this.
const httpsFrameLimit = 64 * 1024

func httpsFrame(reader *bufio.Reader) ([]byte, error) {
	frame, err := reader.ReadSlice('\n')
	if err != nil || len(frame) > httpsFrameLimit {
		return nil, errHTTPSProtocol
	}
	return frame, nil
}
func writeHTTPSResponse(conn net.Conn, id string, result any) error {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > httpsFrameLimit-256 {
		return errHTTPSProtocol
	}
	return json.NewEncoder(conn).Encode(api.Response{Version: api.Version, ID: id, Result: raw})
}
