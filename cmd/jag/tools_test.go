package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
)

func TestToolsOfferedReadsFunctionNames(t *testing.T) {
	body := []byte(`{"model":"qwen3:8b","tools":[
		{"type":"function","function":{"name":"get_light_state","parameters":{}}},
		{"type":"function","function":{"name":"delete_file"}}]}`)
	if got := fmt.Sprint(toolsOffered(body)); got != "[get_light_state delete_file]" {
		t.Fatalf("got %s", got)
	}
	if toolsOffered([]byte(`{"model":"x"}`)) != nil {
		t.Fatal("a request without tools offered some")
	}
}

// The reply llama3.2:3b really gave through ai.local.
func TestToolCallsInAPlainReply(t *testing.T) {
	m := meterWith(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_op25aiad","type":"function",
		"function":{"name":"get_light_state","arguments":"{\"room\":\"living room\"}"}}]},"finish_reason":"tool_calls"}]}`)
	if got := fmt.Sprint(toolCallsOf(m)); got != "[get_light_state]" {
		t.Fatalf("got %s", got)
	}
}

// In a stream a call's name comes in one chunk and its arguments in later
// ones; lines can also be split across writes.
func TestToolCallsInAStreamSplitAcrossWrites(t *testing.T) {
	m := meterWith(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"get_wea",
		"ther\",\"arguments\":\"\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"name\":\"get_time\"}}]}}]}\n\n",
		"data: [DONE]\n\n")
	if got := fmt.Sprint(toolCallsOf(m)); got != "[get_weather get_time]" {
		t.Fatalf("got %s", got)
	}
}

func TestPlainChatHasNoToolCalls(t *testing.T) {
	m := meterWith("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n", "data: [DONE]\n\n")
	if got := toolCallsOf(m); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// The first time an app offers a tool is an alert; the second isn't.
func TestNewToolsAreAlertedOnce(t *testing.T) {
	dir := t.TempDir()
	lg := openToolLedger(dir)
	now := time.Now()
	e := activity.Entry{At: now, App: "Goose", Model: "qwen3:8b", Device: "desktop", ToolsOffered: []string{"read_file", "shell"}}
	if got := lg.observe(e); len(got) != 2 {
		t.Fatalf("first sighting: %+v", got)
	}
	if got := lg.observe(e); len(got) != 0 {
		t.Fatalf("second sighting raised %+v", got)
	}
	e.App = "Open WebUI" // same tool, another app: new power for another app
	if got := lg.observe(e); len(got) != 2 {
		t.Fatalf("another app: %+v", got)
	}
	if got := openToolLedger(dir).recent(now); len(got) != 4 {
		t.Fatalf("after a restart, recent = %d events, want 4", len(got))
	}
	var none *toolLedger
	if none.observe(e) != nil || none.recent(now) != nil {
		t.Fatal("no ledger must be a no-op")
	}
}
