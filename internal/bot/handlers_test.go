package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// A message between two users in a channel that is not an AI channel and does
// not mention Yuna must disappear completely: not stored, not answered and not
// logged. Only triggers are worth a line.
func TestHandleMessageIgnoresUntriggeredChatter(t *testing.T) {
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	logPath := filepath.Join(dir, "yuna.log")
	log, err := applog.New(logPath, true)
	if err != nil {
		t.Fatalf("applog.New: %v", err)
	}
	defer func() { _ = log.Close() }()

	// queue is deliberately nil: were the message answered, submit would panic
	// here, which is exactly the failure this test exists to catch.
	b := &Bot{store: st, log: log, appID: testBotID}

	msg := newMessage()
	msg.ID = "m-1"
	b.handleMessage(msg)

	stored, err := st.MessageBySnowflake(msg.ChannelID, msg.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if stored != nil {
		t.Fatal("a plain message between users was stored")
	}

	raw, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		// No file at all means nothing was written, which is the point.
		return
	}
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(raw), msg.ID) {
		t.Fatalf("an ignored message was logged:\n%s", raw)
	}
}
