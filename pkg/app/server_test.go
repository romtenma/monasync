package app

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romtenma/monasync/pkg/config"
	"github.com/romtenma/monasync/pkg/store"
	"github.com/romtenma/monasync/pkg/syncxml"
)

func TestHandleSyncReturnsForbiddenWhenDailyLimitExceeded(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 1,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	first := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	first.Header.Set("Authorization", basicAuth("user", "pass"))
	firstRecorder := httptest.NewRecorder()
	srv.handleSync(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", firstRecorder.Code)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	second.Header.Set("Authorization", basicAuth("user", "pass"))
	secondRecorder := httptest.NewRecorder()
	srv.handleSync(secondRecorder, second)
	if secondRecorder.Code != http.StatusForbidden {
		t.Fatalf("second status = %d, want 403", secondRecorder.Code)
	}
}

func TestHandleSyncSupportsLegacySyncPath(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	req := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(body))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	rec := httptest.NewRecorder()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/sync", srv.handleSync)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSyncReturnsUnionStatuses(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	initialBody := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/100/" title="server only" read="1" now="1" count="1"/>
    <th id="2" url="https://example.com/test/read.cgi/board/200/" title="shared" read="2" now="2" count="2"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
    <dir name="★★" id_list="2"/>
  </thread_group>
</sync2ch_request>`

	first := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(initialBody))
	first.Header.Set("Authorization", basicAuth("user", "pass"))
	firstRecorder := httptest.NewRecorder()
	srv.handleSync(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", firstRecorder.Code)
	}

	nextBody := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="1" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/200/" title="shared updated" read="3" now="4" count="4"/>
    <th id="2" url="https://example.com/test/read.cgi/board/300/" title="client only" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★★★" id_list="1"/>
    <dir name="★★★★" id_list="2"/>
  </thread_group>
</sync2ch_request>`

	second := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(nextBody))
	second.Header.Set("Authorization", basicAuth("user", "pass"))
	secondRecorder := httptest.NewRecorder()
	srv.handleSync(secondRecorder, second)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", secondRecorder.Code)
	}

	byThread := responseThreadsByURL(t, secondRecorder.Body.Bytes())
	byURL := map[string]string{}
	for url, thread := range byThread {
		byURL[url] = thread.State
	}

	if _, ok := byURL["https://example.com/test/read.cgi/board/100/"]; ok {
		t.Fatal("server-only response thread should be omitted when client is up to date")
	}
	if byURL["https://example.com/test/read.cgi/board/200/"] != "n" {
		t.Fatalf("shared response state = %q, want n", byURL["https://example.com/test/read.cgi/board/200/"])
	}
	if byURL["https://example.com/test/read.cgi/board/300/"] != "n" {
		t.Fatalf("client-only response state = %q, want n", byURL["https://example.com/test/read.cgi/board/300/"])
	}
	if byThread["https://example.com/test/read.cgi/board/200/"].Read != nil {
		t.Fatal("shared response read should be omitted for n")
	}
	if byThread["https://example.com/test/read.cgi/board/300/"].Read != nil {
		t.Fatal("client-only response read should be omitted for n")
	}
}

func TestHandleSyncUsesSyncNumberForDeletionAndRemoteAdd(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body1 := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/100/" title="keep maybe" read="1" now="1" count="1"/>
    <th id="2" url="https://example.com/test/read.cgi/board/200/" title="shared" read="2" now="2" count="2"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
    <dir name="★★" id_list="2"/>
  </thread_group>
</sync2ch_request>`

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body1))
	req1.Header.Set("Authorization", basicAuth("user", "pass"))
	srv.handleSync(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec1.Code)
	}

	body2 := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="1" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/100/" title="keep maybe" read="1" now="1" count="1"/>
    <th id="2" url="https://example.com/test/read.cgi/board/300/" title="remote add" read="5" now="5" count="5"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
    <dir name="★★★" id_list="2"/>
  </thread_group>
</sync2ch_request>`

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body2))
	req2.Header.Set("Authorization", basicAuth("user", "pass"))
	srv.handleSync(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", rec2.Code)
	}

	body3 := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="1" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/200/" title="shared" read="2" now="2" count="2"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body3))
	req3.Header.Set("Authorization", basicAuth("user", "pass"))
	srv.handleSync(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("third status = %d, want 200", rec3.Code)
	}

	byURL := responseThreadsByURL(t, rec3.Body.Bytes())

	if _, ok := byURL["https://example.com/test/read.cgi/board/200/"]; ok {
		t.Fatal("stale client thread deleted on server should be omitted")
	}
	if byURL["https://example.com/test/read.cgi/board/300/"].State != "a" {
		t.Fatalf("remote add response state = %q, want a", byURL["https://example.com/test/read.cgi/board/300/"].State)
	}
	if _, ok := byURL["https://example.com/test/read.cgi/board/100/"]; ok {
		t.Fatal("up-to-date omitted thread should be deleted and omitted")
	}
}

func TestHandleSyncAcceptsGzipRequestBody(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="ChMate" client_version="1.0" os="Android">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	compressed := gzipData(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/sync3", bytes.NewReader(compressed))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	srv.handleSync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<sync2ch_response") {
		t.Fatalf("response body = %q, want sync2ch_response XML", rec.Body.String())
	}
}

func TestHandleSyncReturnsNewResponseForEntityRequest(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	req := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	rec := httptest.NewRecorder()
	srv.handleSync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<entities>") {
		t.Fatalf("new response should contain entities: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<thread_group category="favorite" struct="default">`) {
		t.Fatalf("new response should contain sync3 thread_group: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<th id="0" url="https://example.com/test/read.cgi/board/123/" s="n" />`) {
		t.Fatalf("new response should contain sync3 th element: %s", rec.Body.String())
	}
}

func TestHandleSyncReturnsLegacyResponseWithoutEntities(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="utf-8" ?>
<sync2ch_request sync_number="0" client_version="2.3.1" client_name="2chBrowser" os="Windows 7">
  <thread_group category="favorite">
    <dir name="Folder1">
      <thread url="https://example.com/test/read.cgi/board/777/" title="legacy thread" read="32" now="32" count="523" />
    </dir>
  </thread_group>
</sync2ch_request>`

	req := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	rec := httptest.NewRecorder()
	srv.handleSync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<entities>") {
		t.Fatalf("legacy response should not contain entities: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<thread_group category="favorite">`) {
		t.Fatalf("legacy response should contain thread_group: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<thread `) {
		t.Fatalf("legacy response should contain thread element: %s", rec.Body.String())
	}
}

func TestHandleSyncAcceptsLegacyThreadGroupRequest(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="utf-8" ?>
<sync2ch_request sync_number="0" client_version="2.3.1" client_name="2chBrowser" os="Windows 7">
  <thread_group category="favorite">
    <dir name="Folder1">
      <thread url="https://example.com/test/read.cgi/board/777/" title="legacy thread" read="32" now="32" count="523" />
    </dir>
  </thread_group>
</sync2ch_request>`

	req := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	rec := httptest.NewRecorder()
	srv.handleSync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	byURL := responseThreadsByURL(t, rec.Body.Bytes())
	if _, ok := byURL["https://example.com/test/read.cgi/board/777/"]; !ok {
		t.Fatalf("legacy thread was not reflected in response: %s", rec.Body.String())
	}
}

func TestHandleSyncReturnsGzipResponseWhenRequested(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="ChMate" client_version="1.0" os="Android">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="1" count="1"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	req := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	srv.handleSync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}

	reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer reader.Close()

	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !strings.Contains(string(decoded), "<sync2ch_response") {
		t.Fatalf("decoded response = %q, want sync2ch_response XML", string(decoded))
	}
}

func TestHandleAdminPageShowsStoredThreads(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="2" count="3"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	syncReq := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	syncReq.Header.Set("Authorization", basicAuth("user", "pass"))
	syncRec := httptest.NewRecorder()
	srv.handleSync(syncRec, syncReq)
	if syncRec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200", syncRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicAuth("user", "pass"))
	rec := httptest.NewRecorder()
	srv.handleAdminPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if !strings.Contains(rec.Body.String(), "thread title") {
		t.Fatalf("body = %q, want stored title", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "https://example.com/test/read.cgi/board/123/") {
		t.Fatalf("body = %q, want stored URL", rec.Body.String())
	}
}

func TestHandleDeleteThreadRemovesStoredThread(t *testing.T) {
	st := openAppTestStore(t)
	t.Cleanup(func() {
		_ = st.Close()
	})

	srv := New(config.Config{
		User:       "user",
		Password:   "pass",
		DailyLimit: 30,
	}, st)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<sync2ch_request sync_number="0" client_id="0" sync_rl="post" client_name="Siki" client_version="1.0" os="Windows">
  <entities>
    <th id="1" url="https://example.com/test/read.cgi/board/123/" title="thread title" read="1" now="2" count="3"/>
  </entities>
  <thread_group category="favorite" struct="default">
    <dir name="★" id_list="1"/>
  </thread_group>
</sync2ch_request>`

	syncReq := httptest.NewRequest(http.MethodPost, "/api/sync3", strings.NewReader(body))
	syncReq.Header.Set("Authorization", basicAuth("user", "pass"))
	syncRec := httptest.NewRecorder()
	srv.handleSync(syncRec, syncReq)
	if syncRec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200", syncRec.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodPost, "/threads/delete", strings.NewReader("url=https%3A%2F%2Fexample.com%2Ftest%2Fread.cgi%2Fboard%2F123%2F"))
	deleteReq.Header.Set("Authorization", basicAuth("user", "pass"))
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteRec := httptest.NewRecorder()
	srv.handleDeleteThread(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if location := deleteRec.Header().Get("Location"); !strings.Contains(location, "Thread+deleted") {
		t.Fatalf("Location = %q, want success message", location)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/", nil)
	pageReq.Header.Set("Authorization", basicAuth("user", "pass"))
	pageRec := httptest.NewRecorder()
	srv.handleAdminPage(pageRec, pageReq)

	if pageRec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", pageRec.Code)
	}
	if strings.Contains(pageRec.Body.String(), "thread title") {
		t.Fatalf("body = %q, thread should be removed", pageRec.Body.String())
	}
	if !strings.Contains(pageRec.Body.String(), "No stored threads yet.") {
		t.Fatalf("body = %q, want empty state", pageRec.Body.String())
	}
}

func openAppTestStore(t *testing.T) *store.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "app-test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

func basicAuth(username string, password string) string {
	credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + credentials
}

func gzipData(t *testing.T, raw string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(raw)); err != nil {
		t.Fatalf("writer.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	return buffer.Bytes()
}

func responseThreadsByURL(t *testing.T, payload []byte) map[string]syncxml.ResponseThread {
	t.Helper()

	var response syncxml.Response
	if err := xml.Unmarshal(payload, &response); err == nil && len(response.Entities) > 0 {
		byURL := map[string]syncxml.ResponseThread{}
		for _, entity := range response.Entities {
			for _, thread := range entity.Threads {
				byURL[thread.URL] = thread
			}
		}
		return byURL
	}

	var legacy syncxml.LegacyResponse
	if err := xml.Unmarshal(payload, &legacy); err == nil && len(legacy.ThreadGroup) > 0 {
		byURL := map[string]syncxml.ResponseThread{}
		for _, group := range legacy.ThreadGroup {
			collectLegacyThreads(group, byURL)
		}
		return byURL
	}

	t.Fatalf("unable to parse response payload: %s", string(payload))
	return nil
}

func collectLegacyThreads(dir syncxml.LegacyResponseDir, byURL map[string]syncxml.ResponseThread) {
	for _, thread := range dir.Threads {
		byURL[thread.URL] = syncxml.ResponseThread{
			URL:   thread.URL,
			Title: thread.Title,
			Read:  thread.Read,
			Now:   thread.Now,
			Count: thread.Count,
			State: thread.State,
		}
	}
	for _, child := range dir.Dirs {
		collectLegacyThreads(child, byURL)
	}
}
