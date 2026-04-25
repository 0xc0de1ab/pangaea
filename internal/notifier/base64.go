package notifier

import "encoding/base64"

// base64Decode mirrors mediator's encoder; standard padded base64.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
