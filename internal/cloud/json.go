package cloud

import (
	"encoding/base64"
	"encoding/json"
	"io"
)

type jsonRaw = json.RawMessage

func decode(b []byte, v any) error { return json.Unmarshal(b, v) }
func marshal(v any) []byte         { b, _ := json.Marshal(v); return b }

func decodeReader(r io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(r, 8<<20)).Decode(v)
}
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
