package tunnel

import "github.com/0xc0de1ab/pangaea/internal/compat"

type DataFrameType string

const (
	DataFrameRequest  DataFrameType = "request"
	DataFrameCancel   DataFrameType = "cancel"
	DataFrameEvent    DataFrameType = "event"
	DataFrameResponse DataFrameType = "response"
)

type DataRequest struct {
	Type            DataFrameType    `json:"type,omitempty"`
	RequestID       string           `json:"request_id"`
	Descriptor      StreamDescriptor `json:"descriptor"`
	CapabilityToken string           `json:"capability_token"`
	Request         compat.Request   `json:"request"`
}

type DataResponse struct {
	Type      DataFrameType   `json:"type,omitempty"`
	RequestID string          `json:"request_id"`
	StreamID  StreamID        `json:"stream_id"`
	Event     compat.Event    `json:"event,omitempty"`
	Response  compat.Response `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}
