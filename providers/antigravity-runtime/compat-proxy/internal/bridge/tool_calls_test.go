package bridge

import "testing"

func TestExtractToolCallsFromAntigravityTopLevelIR(t *testing.T) {
	payload := []byte(`{
		"response":"",
		"tool_calls":[
			{"id":"call_1","function":{"name":"write_file","arguments":{"path":"a.yaml","content":"ok"}}}
		]
	}`)
	calls := extractToolCallsFromPayload(payload)
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	if calls[0].Function.Name != "write_file" || calls[0].Function.Arguments != `{"content":"ok","path":"a.yaml"}` {
		t.Fatalf("unexpected tool call: %#v", calls[0])
	}
}

func TestExtractToolCallsFromStreamGenerateContentFunctionCall(t *testing.T) {
	payload := []byte(`{
		"response":{
			"candidates":[{
				"content":{
					"parts":[{
						"functionCall":{"name":"lookup_go_version","args":{"host":"kind-antigravity"}}
					}]
				}
			}]
		}
	}`)
	calls := extractToolCallsFromPayload(payload)
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	if calls[0].Function.Name != "lookup_go_version" || calls[0].Function.Arguments != `{"host":"kind-antigravity"}` {
		t.Fatalf("unexpected tool call: %#v", calls[0])
	}
}

func TestExtractToolCallsInfersPatchPayload(t *testing.T) {
	payload := []byte(`{
		"response":{
			"patch":"*** Begin Patch\n--- f.html\n+++ f.html\n@@ -1 +1 @@\n-old\n+new\n*** End Patch\n",
			"intent":"f.html 파일에 텍스처 폴백 로직을 추가합니다."
		}
	}`)
	calls := extractToolCallsFromPayload(payload)
	if len(calls) != 1 {
		t.Fatalf("expected one inferred patch call, got %#v", calls)
	}
	if calls[0].Function.Name != "apply_patch" {
		t.Fatalf("unexpected tool call: %#v", calls[0])
	}
	if calls[0].Function.Arguments == "" || calls[0].Function.Arguments == "{}" {
		t.Fatalf("patch arguments were not preserved: %#v", calls[0])
	}
}
