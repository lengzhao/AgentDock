package weixin

import (
	"context"
	"testing"
	"time"
)

func TestWeixinMetadataUsesKnownChatTypeAndServerTimestamp(t *testing.T) {
	msg := incoming("1", "owner", "hello")
	msg.CreateTimeMS = time.Date(2030, 1, 2, 3, 4, 5, 123000000, time.UTC).UnixMilli()
	platform := newTestPlatform(t, &mockTransport{batches: []Updates{{Messages: []Message{msg}}}})
	event, err := platform.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Envelope.Metadata["chat_type"] != "direct" || event.Envelope.Metadata["message_timestamp"] != "2030-01-02T03:04:05.123Z" {
		t.Fatalf("metadata: %#v", event.Envelope.Metadata)
	}
	for _, key := range []string{"sender_name", "sender_email"} {
		if _, exists := event.Envelope.Metadata[key]; exists {
			t.Fatalf("fabricated profile %s", key)
		}
	}
}
func TestWeixinMetadataOmitsUnknownMessageTimestamp(t *testing.T) {
	platform := newTestPlatform(t, &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "hello")}}}})
	event, err := platform.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := event.Envelope.Metadata["message_timestamp"]; exists {
		t.Fatal("fabricated server timestamp")
	}
}
