package extserver

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
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
	out = appendBytesField(out, 1, []byte(metadataEnv("ANTIGRAVITY_IDE_NAME", "antigravity")))
	if ideVersion := metadataEnv("ANTIGRAVITY_IDE_VERSION", metadataEnv("PANGAEA_TARGET_VERSION", "")); ideVersion != "" {
		out = appendBytesField(out, 7, []byte(ideVersion))
	}
	out = appendBytesField(out, 12, []byte(metadataEnv("ANTIGRAVITY_EXTENSION_NAME", "antigravity")))
	if extensionPath := metadataEnv("ANTIGRAVITY_EXTENSION_PATH", "/opt/antigravity-server/extensions/antigravity"); extensionPath != "" {
		out = appendBytesField(out, 17, []byte(extensionPath))
	}
	out = appendBytesField(out, 4, []byte(metadataLocale()))
	if deviceFingerprint := strings.TrimSpace(os.Getenv("ANTIGRAVITY_DEVICE_FINGERPRINT")); deviceFingerprint != "" {
		out = appendBytesField(out, 24, []byte(deviceFingerprint))
	}
	out = appendBytesField(out, 3, []byte(apiKey))
	if disableTelemetry := strings.TrimSpace(os.Getenv("ANTIGRAVITY_DISABLE_TELEMETRY")); disableTelemetry == "1" || strings.EqualFold(disableTelemetry, "true") {
		out = appendBoolField(out, 6, true)
	}
	if userTierID := strings.TrimSpace(os.Getenv("ANTIGRAVITY_USER_TIER_ID")); userTierID != "" {
		out = appendBytesField(out, 29, []byte(userTierID))
	}
	return out
}

func EncodeCodeiumMetadataForProcess(apiKey string) []byte {
	return encodeCodeiumMetadata(apiKey)
}

func metadataEnv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func metadataLocale() string {
	value := metadataEnv("ANTIGRAVITY_LOCALE", "")
	if value == "" {
		value = metadataEnv("LANGUAGE", "")
	}
	if value == "" {
		value = metadataEnv("LANG", "")
	}
	value = strings.TrimSpace(strings.Split(value, ".")[0])
	value = strings.TrimSpace(strings.Split(value, "_")[0])
	if value == "" || strings.EqualFold(value, "C") {
		return "en"
	}
	return value
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
