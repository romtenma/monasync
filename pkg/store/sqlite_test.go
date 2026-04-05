package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/romtenma/monasync/pkg/syncxml"
)

func TestReplaceSnapshotPreservesFieldsAndFallbackTitle(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	firstReq := syncxml.Request{
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{{
			ID:    "1",
			URL:   "https://example.com/test/read.cgi/board/123/",
			Title: "thread title",
			Read:  10,
			Now:   5,
			Count: 20,
		}}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{{Name: "★★", IDList: "1"}}},
	}

	records, _, err := st.ReplaceSnapshot(ctx, "user", firstReq, 30)
	if err != nil {
		t.Fatalf("first ReplaceSnapshot: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records length = %d, want 1", len(records))
	}

	secondReq := syncxml.Request{
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{{
			ID:    "1",
			URL:   "https://example.com/test/read.cgi/board/123/",
			Title: "",
			Read:  3,
			Now:   8,
			Count: 15,
		}}},
		ThreadGroup: syncxml.RequestGroup{},
	}

	records, _, err = st.ReplaceSnapshot(ctx, "user", secondReq, 30)
	if err != nil {
		t.Fatalf("second ReplaceSnapshot: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records length = %d, want 1", len(records))
	}

	record := records[0]
	if record.Title != "thread title" {
		t.Fatalf("title = %q, want fallback title", record.Title)
	}
	if record.State != "u" {
		t.Fatalf("state = %q, want u", record.State)
	}
	if record.Read != 10 {
		t.Fatalf("read = %d, want 10", record.Read)
	}
	if record.Now != 8 {
		t.Fatalf("now = %d, want 8", record.Now)
	}
	if record.Count != 20 {
		t.Fatalf("count = %d, want 20", record.Count)
	}
	if record.Dir != "★★" {
		t.Fatalf("dir = %q, want original dir fallback", record.Dir)
	}
}

func TestReplaceSnapshotDailyLimit(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	req := syncxml.Request{
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{{
			ID:    "1",
			URL:   "https://example.com/test/read.cgi/board/123/",
			Title: "thread title",
			Read:  1,
			Now:   1,
			Count: 1,
		}}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{{Name: "★", IDList: "1"}}},
	}

	_, state, err := st.ReplaceSnapshot(ctx, "user", req, 2)
	if err != nil {
		t.Fatalf("first ReplaceSnapshot: %v", err)
	}
	if state.Remain != 1 {
		t.Fatalf("first remain = %d, want 1", state.Remain)
	}

	_, state, err = st.ReplaceSnapshot(ctx, "user", req, 2)
	if err != nil {
		t.Fatalf("second ReplaceSnapshot: %v", err)
	}
	if state.Remain != 0 {
		t.Fatalf("second remain = %d, want 0", state.Remain)
	}

	_, _, err = st.ReplaceSnapshot(ctx, "user", req, 2)
	if err == nil {
		t.Fatal("third ReplaceSnapshot error = nil, want ErrSyncLimitExceeded")
	}
	if err != ErrSyncLimitExceeded {
		t.Fatalf("third ReplaceSnapshot error = %v, want %v", err, ErrSyncLimitExceeded)
	}

	restore := withFixedNow(time.Date(2026, 4, 5, 12, 0, 0, 0, time.Local))
	defer restore()

	_, state, err = st.ReplaceSnapshot(ctx, "user", req, 2)
	if err != nil {
		t.Fatalf("next day ReplaceSnapshot: %v", err)
	}
	if state.Remain != 1 {
		t.Fatalf("next day remain = %d, want 1", state.Remain)
	}
}

func TestReplaceSnapshotBuildsUnionStatuses(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	initialReq := syncxml.Request{
		SyncNumber: 0,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "1", URL: "https://example.com/test/read.cgi/board/100/", Title: "server only", Read: 2, Now: 2, Count: 3},
			{ID: "2", URL: "https://example.com/test/read.cgi/board/200/", Title: "shared", Read: 4, Now: 5, Count: 6},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{
			{Name: "★", IDList: "1"},
			{Name: "★★", IDList: "2"},
		}},
	}

	if _, _, err := st.ReplaceSnapshot(ctx, "user", initialReq, 30); err != nil {
		t.Fatalf("initial ReplaceSnapshot: %v", err)
	}

	nextReq := syncxml.Request{
		SyncNumber: 1,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "1", URL: "https://example.com/test/read.cgi/board/200/", Title: "shared updated", Read: 3, Now: 7, Count: 5},
			{ID: "2", URL: "https://example.com/test/read.cgi/board/300/", Title: "client only", Read: 1, Now: 1, Count: 1},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{
			{Name: "★★★", IDList: "1"},
			{Name: "★★★★", IDList: "2"},
		}},
	}

	records, _, err := st.ReplaceSnapshot(ctx, "user", nextReq, 30)
	if err != nil {
		t.Fatalf("next ReplaceSnapshot: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records length = %d, want 2", len(records))
	}

	byURL := map[string]ThreadRecord{}
	for _, record := range records {
		byURL[record.URL] = record
	}

	if _, ok := byURL["https://example.com/test/read.cgi/board/100/"]; ok {
		t.Fatal("server-only thread should be deleted when client is up to date")
	}
	if byURL["https://example.com/test/read.cgi/board/200/"].State != "n" {
		t.Fatalf("shared state = %q, want n", byURL["https://example.com/test/read.cgi/board/200/"].State)
	}
	if byURL["https://example.com/test/read.cgi/board/300/"].State != "n" {
		t.Fatalf("client-only state = %q, want n", byURL["https://example.com/test/read.cgi/board/300/"].State)
	}
	if byURL["https://example.com/test/read.cgi/board/200/"].Now != 7 {
		t.Fatalf("shared now = %d, want 7", byURL["https://example.com/test/read.cgi/board/200/"].Now)
	}
	if byURL["https://example.com/test/read.cgi/board/200/"].Read != 4 {
		t.Fatalf("shared read = %d, want 4", byURL["https://example.com/test/read.cgi/board/200/"].Read)
	}
	if byURL["https://example.com/test/read.cgi/board/300/"].Read != 1 {
		t.Fatalf("client-only read = %d, want 1", byURL["https://example.com/test/read.cgi/board/300/"].Read)
	}
}

func TestReplaceSnapshotUsesSyncNumberForRemoteAddAndDelete(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	baseReq := syncxml.Request{
		SyncNumber: 0,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "1", URL: "https://example.com/test/read.cgi/board/100/", Title: "keep maybe", Read: 1, Now: 1, Count: 1},
			{ID: "2", URL: "https://example.com/test/read.cgi/board/200/", Title: "shared", Read: 2, Now: 2, Count: 2},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{
			{Name: "★", IDList: "1"},
			{Name: "★★", IDList: "2"},
		}},
	}

	_, state1, err := st.ReplaceSnapshot(ctx, "user", baseReq, 30)
	if err != nil {
		t.Fatalf("base ReplaceSnapshot: %v", err)
	}

	remoteUpdateReq := syncxml.Request{
		SyncNumber: state1.SyncNumber,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "1", URL: "https://example.com/test/read.cgi/board/100/", Title: "keep maybe", Read: 1, Now: 1, Count: 1},
			{ID: "2", URL: "https://example.com/test/read.cgi/board/300/", Title: "remote add", Read: 5, Now: 5, Count: 5},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{
			{Name: "★", IDList: "1"},
			{Name: "★★★", IDList: "2"},
		}},
	}

	_, state2, err := st.ReplaceSnapshot(ctx, "user", remoteUpdateReq, 30)
	if err != nil {
		t.Fatalf("remote update ReplaceSnapshot: %v", err)
	}

	staleReq := syncxml.Request{
		SyncNumber: state1.SyncNumber,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "1", URL: "https://example.com/test/read.cgi/board/200/", Title: "shared stale", Read: 2, Now: 2, Count: 2},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{{Name: "★★", IDList: "1"}}},
	}

	records, state3, err := st.ReplaceSnapshot(ctx, "user", staleReq, 30)
	if err != nil {
		t.Fatalf("stale ReplaceSnapshot: %v", err)
	}
	if state2.SyncNumber >= state3.SyncNumber {
		t.Fatalf("sync number did not advance: before=%d after=%d", state2.SyncNumber, state3.SyncNumber)
	}

	byURL := map[string]ThreadRecord{}
	for _, record := range records {
		byURL[record.URL] = record
	}

	if _, ok := byURL["https://example.com/test/read.cgi/board/200/"]; ok {
		t.Fatal("server-deleted stale thread should not be restored")
	}
	if byURL["https://example.com/test/read.cgi/board/300/"].State != "a" {
		t.Fatalf("remote add state = %q, want a", byURL["https://example.com/test/read.cgi/board/300/"].State)
	}
	if _, ok := byURL["https://example.com/test/read.cgi/board/100/"]; ok {
		t.Fatal("up-to-date omission should delete thread 100")
	}
}

func TestListThreadsReturnsSortedRecords(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	req := syncxml.Request{
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{
			{ID: "2", URL: "https://example.com/test/read.cgi/board/200/", Title: "thread 200", Read: 2, Now: 2, Count: 2},
			{ID: "1", URL: "https://example.com/test/read.cgi/board/100/", Title: "thread 100", Read: 1, Now: 1, Count: 1},
		}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{
			{Name: "★★", IDList: "2"},
			{Name: "★", IDList: "1"},
		}},
	}

	if _, _, err := st.ReplaceSnapshot(ctx, "user", req, 30); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}

	records, err := st.ListThreads(ctx, "user")
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records length = %d, want 2", len(records))
	}
	if records[0].Dir != "★" || records[0].URL != "https://example.com/test/read.cgi/board/100/" {
		t.Fatalf("first record = %+v, want ★ / 100", records[0])
	}
	if records[1].Dir != "★★" || records[1].URL != "https://example.com/test/read.cgi/board/200/" {
		t.Fatalf("second record = %+v, want ★★ / 200", records[1])
	}
}

func TestDeleteThreadRemovesThreadAndCreatesTombstone(t *testing.T) {
	t.Cleanup(withFixedNow(time.Date(2026, 4, 4, 12, 0, 0, 0, time.Local)))

	st := openTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	req := syncxml.Request{
		SyncNumber: 0,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{{
			ID:    "1",
			URL:   "https://example.com/test/read.cgi/board/123/",
			Title: "thread title",
			Read:  1,
			Now:   1,
			Count: 1,
		}}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{{Name: "★", IDList: "1"}}},
	}

	_, state, err := st.ReplaceSnapshot(ctx, "user", req, 30)
	if err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}

	result, err := st.DeleteThread(ctx, "user", "https://example.com/test/read.cgi/board/123/")
	if err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if !result.Deleted {
		t.Fatal("DeleteThread deleted = false, want true")
	}
	if result.Sync <= state.SyncNumber {
		t.Fatalf("DeleteThread sync = %d, want > %d", result.Sync, state.SyncNumber)
	}

	records, err := st.ListThreads(ctx, "user")
	if err != nil {
		t.Fatalf("ListThreads after delete: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records length after delete = %d, want 0", len(records))
	}

	staleReq := syncxml.Request{
		SyncNumber: state.SyncNumber,
		Entities: syncxml.RequestItems{Threads: []syncxml.RequestThread{{
			ID:    "1",
			URL:   "https://example.com/test/read.cgi/board/123/",
			Title: "thread title",
			Read:  1,
			Now:   1,
			Count: 1,
		}}},
		ThreadGroup: syncxml.RequestGroup{Dirs: []syncxml.RequestDir{{Name: "★", IDList: "1"}}},
	}

	records, _, err = st.ReplaceSnapshot(ctx, "user", staleReq, 30)
	if err != nil {
		t.Fatalf("ReplaceSnapshot after admin delete: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records length after stale re-sync = %d, want 0", len(records))
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func withFixedNow(fixed time.Time) func() {
	previous := nowFunc
	nowFunc = func() time.Time {
		return fixed
	}
	return func() {
		nowFunc = previous
	}
}
