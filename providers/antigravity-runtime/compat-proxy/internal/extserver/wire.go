package extserver

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const connectContentType = "application/connect+proto"

func readRequestPayload(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/connect+") {
		return body, nil
	}
	if len(body) < 5 {
		return nil, fmt.Errorf("connect frame too short: %d", len(body))
	}
	length := int(binary.BigEndian.Uint32(body[1:5]))
	if length < 0 || len(body)-5 < length {
		return nil, fmt.Errorf("connect frame length %d exceeds body length %d", length, len(body)-5)
	}
	return body[5 : 5+length], nil
}

func writeConnectFrame(w http.ResponseWriter, payload []byte) error {
	w.Header().Set("Content-Type", connectContentType)
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func appendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func appendBytesField(dst []byte, fieldNumber int, value []byte) []byte {
	dst = appendVarint(dst, uint64(fieldNumber<<3|2))
	dst = appendVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendBoolField(dst []byte, fieldNumber int, value bool) []byte {
	if !value {
		return dst
	}
	dst = appendVarint(dst, uint64(fieldNumber<<3|0))
	return appendVarint(dst, 1)
}

func readStringField(payload []byte, fieldNumber int) (string, bool) {
	value, ok := readBytesField(payload, fieldNumber)
	if !ok {
		return "", false
	}
	return string(value), true
}

func readBytesField(payload []byte, fieldNumber int) ([]byte, bool) {
	for len(payload) > 0 {
		tag, n, ok := consumeVarint(payload)
		if !ok {
			return nil, false
		}
		payload = payload[n:]
		number := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch wireType {
		case 0:
			_, n, ok := consumeVarint(payload)
			if !ok {
				return nil, false
			}
			payload = payload[n:]
		case 2:
			length, n, ok := consumeVarint(payload)
			if !ok || int(length) > len(payload[n:]) {
				return nil, false
			}
			value := payload[n : n+int(length)]
			payload = payload[n+int(length):]
			if number == fieldNumber {
				return value, true
			}
		default:
			return nil, false
		}
	}
	return nil, false
}

func readBoolField(payload []byte, fieldNumber int) bool {
	for len(payload) > 0 {
		tag, n, ok := consumeVarint(payload)
		if !ok {
			return false
		}
		payload = payload[n:]
		number := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch wireType {
		case 0:
			value, n, ok := consumeVarint(payload)
			if !ok {
				return false
			}
			payload = payload[n:]
			if number == fieldNumber {
				return value != 0
			}
		case 2:
			length, n, ok := consumeVarint(payload)
			if !ok || int(length) > len(payload[n:]) {
				return false
			}
			payload = payload[n+int(length):]
		default:
			return false
		}
	}
	return false
}

func consumeVarint(payload []byte) (uint64, int, bool) {
	var value uint64
	for i, b := range payload {
		if i >= 10 {
			return 0, 0, false
		}
		value |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return value, i + 1, true
		}
	}
	return 0, 0, false
}

func encodeUnifiedStateInitialUpdate(initialState []byte) []byte {
	return appendBytesField(nil, 1, initialState)
}

func encodeCodeiumMetadata(apiKey string) []byte {
	var out []byte
	out = appendBytesField(out, 1, []byte("antigravity-compat-proxy"))
	out = appendBytesField(out, 3, []byte(apiKey))
	out = appendBytesField(out, 4, []byte("en"))
	out = appendBoolField(out, 6, true)
	out = appendBytesField(out, 7, []byte("headless"))
	out = appendBytesField(out, 12, []byte("antigravity"))
	return out
}

func EncodeCodeiumMetadataForProcess(apiKey string) []byte {
	return encodeCodeiumMetadata(apiKey)
}

func upsertInitialStateRow(initialState []byte, key string, newRow []byte, deleted bool) []byte {
	var out []byte
	found := false
	for offset := 0; offset < len(initialState); {
		start := offset
		tag, n, ok := consumeVarint(initialState[offset:])
		if !ok {
			return initialState
		}
		offset += n
		number := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch wireType {
		case 0:
			_, n, ok := consumeVarint(initialState[offset:])
			if !ok {
				return initialState
			}
			offset += n
		case 2:
			length, n, ok := consumeVarint(initialState[offset:])
			if !ok || int(length) > len(initialState[offset+n:]) {
				return initialState
			}
			valueStart := offset + n
			valueEnd := valueStart + int(length)
			value := initialState[valueStart:valueEnd]
			offset = valueEnd
			if number == 1 {
				rowKey, _ := readStringField(value, 1)
				if rowKey == key {
					found = true
					if !deleted {
						out = appendBytesField(out, 1, newRow)
					}
					continue
				}
			}
		default:
			return initialState
		}
		out = append(out, initialState[start:offset]...)
	}
	if !found && !deleted && len(newRow) > 0 {
		out = appendBytesField(out, 1, newRow)
	}
	return out
}
