package backup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dylaris-core/models"
)

// The "connection" provider must never carry credentials of its own: it
// references a saved storage connection so a rotated key takes effect
// everywhere at once. These pin the contract Open enforces.
func TestOpen_Connection(t *testing.T) {
	type call struct {
		id     int
		prefix string
	}

	newDeps := func(seen *call) Deps {
		return Deps{Connection: func(id int, prefix string) (Storage, error) {
			*seen = call{id: id, prefix: prefix}
			return nil, nil
		}}
	}

	t.Run("passes the referenced id and prefix through", func(t *testing.T) {
		var seen call
		bs := &models.BackupStorage{Provider: "connection",
			Config: json.RawMessage(`{"connectionId":7,"prefix":"archives"}`)}
		if _, err := Open(context.Background(), bs, newDeps(&seen)); err != nil {
			t.Fatalf("Open: %v", err)
		}
		if seen.id != 7 || seen.prefix != "archives" {
			t.Fatalf("resolved %+v, want id 7 prefix archives", seen)
		}
	})

	// A connection shared with Core file storage would otherwise drop archives
	// in the bucket root beside library/ and modpacks/, which makes per-subsystem
	// lifecycle rules and quota accounting impossible after the fact.
	t.Run("defaults the prefix to the server-backups namespace", func(t *testing.T) {
		for _, raw := range []string{`{"connectionId":2}`, `{"connectionId":2,"prefix":"  "}`} {
			var seen call
			bs := &models.BackupStorage{Provider: "connection", Config: json.RawMessage(raw)}
			if _, err := Open(context.Background(), bs, newDeps(&seen)); err != nil {
				t.Fatalf("Open(%s): %v", raw, err)
			}
			if seen.prefix != CoreStorageSubPrefix {
				t.Errorf("Open(%s) prefix = %q, want %q", raw, seen.prefix, CoreStorageSubPrefix)
			}
		}
	})

	t.Run("refuses a row with no connection referenced", func(t *testing.T) {
		var seen call
		for _, raw := range []string{`{}`, `{"connectionId":0}`, `{"connectionId":-1}`} {
			bs := &models.BackupStorage{Provider: "connection", Config: json.RawMessage(raw)}
			_, err := Open(context.Background(), bs, newDeps(&seen))
			if err == nil || !strings.Contains(err.Error(), "connectionId") {
				t.Errorf("Open(%s) err = %v, want a connectionId complaint", raw, err)
			}
		}
	})

	// Without the builder the caller cannot resolve anything; failing loudly
	// beats a nil Storage surfacing later as a mid-backup panic.
	t.Run("refuses when no builder was wired", func(t *testing.T) {
		bs := &models.BackupStorage{Provider: "connection",
			Config: json.RawMessage(`{"connectionId":1}`)}
		if _, err := Open(context.Background(), bs, Deps{}); err == nil {
			t.Fatal("Open with no Connection builder should fail")
		}
	})

	t.Run("surfaces the builder's error", func(t *testing.T) {
		boom := errors.New("connection 4 could not be loaded")
		bs := &models.BackupStorage{Provider: "connection",
			Config: json.RawMessage(`{"connectionId":4}`)}
		_, err := Open(context.Background(), bs, Deps{
			Connection: func(int, string) (Storage, error) { return nil, boom },
		})
		if err == nil || !strings.Contains(err.Error(), boom.Error()) {
			t.Fatalf("err = %v, want it to wrap %v", err, boom)
		}
	})
}
