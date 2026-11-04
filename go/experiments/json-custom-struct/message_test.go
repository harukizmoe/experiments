package jsoncustomstruct

import (
	"encoding/json"
	"testing"
)

func TestMessages(t *testing.T) {
	raw := []byte(`[
		{"role": "user", "content": [
			{"type": "text", "text": "这张图里是什么"},
			{"type": "image", "data": "AAAA", "mimeType": "image/png"}
		]},
		{"role": "assistant", "content":[{"type":"text","text":" 让我看看"}], "stopReason":"tool_use"}
	]`)

	var ml MessageList
	if err := json.Unmarshal(raw, &ml); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Logf("ml: %#v", ml)
}
