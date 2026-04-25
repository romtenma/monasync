package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/romtenma/monasync/pkg/syncxml"
)

type Store struct {
	db *sql.DB
}

var ErrSyncLimitExceeded = errors.New("sync exhausted")

var nowFunc = time.Now

type ClientState struct {
	ClientID    int64
	SyncNumber  int64
	Remain      int64
	AccountType string
}

type clientRow struct {
	ClientState
	SyncDay   string
	SyncCount int64
}

type ThreadRecord struct {
	URL   string
	Title string
	Dir   string
	Read  int64
	Now   int64
	Count int64
	State string
	Sync  int64
}

type DeleteThreadResult struct {
	Deleted bool
	Sync    int64
}

type DeletedThreadRecord struct {
	URL  string
	Sync int64
}

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(10000)"
	} else if !strings.Contains(dsn, "busy_timeout") {
		dsn += "&_pragma=busy_timeout(10000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ReplaceSnapshot(ctx context.Context, username string, req syncxml.Request, dailyLimit int64) ([]ThreadRecord, ClientState, error) {
	tx, err := retryOnLocked(ctx, func() (*sql.Tx, error) {
		return s.db.BeginTx(ctx, nil)
	})
	if err != nil {
		return nil, ClientState{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	clientState, err := loadOrCreateClient(ctx, tx, username, req.ClientID, req.SyncNumber, dailyLimit, nowFunc())
	if err != nil {
		return nil, ClientState{}, err
	}

	dirByID := make(map[string]string, len(req.Entities.Threads))
	for _, dir := range req.ThreadGroup.Dirs {
		name := normalizeDirName(dir.Name)
		for _, id := range splitIDs(dir.IDList) {
			dirByID[id] = name
		}
	}

	existing, err := loadExistingThreads(ctx, tx, username)
	if err != nil {
		return nil, ClientState{}, err
	}
	deleted, err := loadDeletedThreads(ctx, tx, username)
	if err != nil {
		return nil, ClientState{}, err
	}

	requestSyncNumber := req.SyncNumber
	currentSyncNumber := clientState.SyncNumber

	recordsByURL := make(map[string]ThreadRecord, len(existing)+len(req.Entities.Threads))
	deletedByURL := make(map[string]DeletedThreadRecord, len(deleted))
	for url, record := range deleted {
		deletedByURL[url] = record
	}
	seen := make(map[string]struct{}, len(req.Entities.Threads))
	for _, thread := range req.Entities.Threads {
		if strings.TrimSpace(thread.URL) == "" {
			continue
		}
		url := strings.TrimSpace(thread.URL)

		record := ThreadRecord{
			URL:   url,
			Title: strings.TrimSpace(thread.Title),
			Dir:   strings.TrimSpace(dirByID[thread.ID]),
			Read:  thread.Read,
			Now:   thread.Now,
			Count: thread.Count,
			State: "n",
			Sync:  currentSyncNumber,
		}

		if prev, ok := existing[record.URL]; ok {
			clientRecord := normalizeComparableRecord(record)
			record = mergeThreadRecord(prev, record)
			record.Dir = normalizeDirName(record.Dir)
			serverChanged := prev.Sync > requestSyncNumber
			recordChanged := threadPayloadChanged(prev, record)
			if recordChanged {
				record.Sync = currentSyncNumber
			} else {
				record.Sync = prev.Sync
			}
			if serverChanged && threadPayloadChanged(record, clientRecord) {
				record.State = "u"
			}
			delete(deletedByURL, record.URL)
		} else if tombstone, ok := deletedByURL[record.URL]; ok && tombstone.Sync > requestSyncNumber {
			seen[record.URL] = struct{}{}
			continue
		} else {
			record.Dir = normalizeDirName(record.Dir)
			delete(deletedByURL, record.URL)
		}

		if record.Title == "" {
			continue
		}

		recordsByURL[record.URL] = record
		seen[record.URL] = struct{}{}
	}

	for url, record := range existing {
		if _, ok := seen[url]; ok {
			continue
		}
		if record.Sync > requestSyncNumber {
			record.State = "a"
			recordsByURL[url] = record
			continue
		}
		deletedByURL[url] = DeletedThreadRecord{URL: url, Sync: currentSyncNumber}
	}

	records := make([]ThreadRecord, 0, len(recordsByURL))
	for _, record := range recordsByURL {
		records = append(records, record)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE username = ?`, username); err != nil {
		return nil, ClientState{}, fmt.Errorf("clear snapshot: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO threads (username, url, title, dir_name, read_value, now_value, count_value, modified_sync, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, ClientState{}, fmt.Errorf("prepare insert thread: %w", err)
	}
	defer stmt.Close()

	now := nowFunc().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		if _, err := stmt.ExecContext(ctx, username, record.URL, record.Title, record.Dir, record.Read, record.Now, record.Count, record.Sync, now); err != nil {
			return nil, ClientState{}, fmt.Errorf("insert thread: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM deleted_threads WHERE username = ?`, username); err != nil {
		return nil, ClientState{}, fmt.Errorf("clear deleted threads: %w", err)
	}

	deletedStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO deleted_threads (username, url, deleted_sync, updated_at)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return nil, ClientState{}, fmt.Errorf("prepare insert deleted thread: %w", err)
	}
	defer deletedStmt.Close()

	for _, record := range deletedByURL {
		if _, err := deletedStmt.ExecContext(ctx, username, record.URL, record.Sync, now); err != nil {
			return nil, ClientState{}, fmt.Errorf("insert deleted thread: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, ClientState{}, fmt.Errorf("commit tx: %w", err)
	}

	sort.Slice(records, func(i int, j int) bool {
		if records[i].Dir == records[j].Dir {
			return records[i].URL < records[j].URL
		}
		return records[i].Dir < records[j].Dir
	})

	return records, clientState, nil
}

func (s *Store) HealthCheck(ctx context.Context) error {
	_, err := retryOnLocked(ctx, func() (struct{}, error) {
		return struct{}{}, s.db.PingContext(ctx)
	})
	return err
}

func (s *Store) ListThreads(ctx context.Context, username string) ([]ThreadRecord, error) {
	rows, err := retryOnLocked(ctx, func() (*sql.Rows, error) {
		return s.db.QueryContext(ctx, `
			SELECT url, title, dir_name, read_value, now_value, count_value, modified_sync
			FROM threads
			WHERE username = ?
			ORDER BY dir_name, url
		`, username)
	})
	if err != nil {
		return nil, fmt.Errorf("query threads: %w", err)
	}
	defer rows.Close()

	threads := make([]ThreadRecord, 0)
	for rows.Next() {
		var record ThreadRecord
		if err := rows.Scan(&record.URL, &record.Title, &record.Dir, &record.Read, &record.Now, &record.Count, &record.Sync); err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		threads = append(threads, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate threads: %w", err)
	}

	return threads, nil
}

func (s *Store) DeleteThread(ctx context.Context, username string, url string) (DeleteThreadResult, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return DeleteThreadResult{}, nil
	}

	tx, err := retryOnLocked(ctx, func() (*sql.Tx, error) {
		return s.db.BeginTx(ctx, nil)
	})
	if err != nil {
		return DeleteThreadResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var currentSync sql.NullInt64
	row := tx.QueryRowContext(ctx, `SELECT sync_number FROM clients WHERE username = ?`, username)
	if err := row.Scan(&currentSync); err != nil && err != sql.ErrNoRows {
		return DeleteThreadResult{}, fmt.Errorf("load client sync: %w", err)
	}

	deleteResult, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE username = ? AND url = ?`, username, url)
	if err != nil {
		return DeleteThreadResult{}, fmt.Errorf("delete thread: %w", err)
	}

	affected, err := deleteResult.RowsAffected()
	if err != nil {
		return DeleteThreadResult{}, fmt.Errorf("thread rows affected: %w", err)
	}
	if affected == 0 {
		return DeleteThreadResult{}, tx.Commit()
	}

	nextSync := currentSync.Int64 + 1
	now := nowFunc().UTC().Format(time.RFC3339Nano)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deleted_threads (username, url, deleted_sync, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(username, url) DO UPDATE SET
			deleted_sync = excluded.deleted_sync,
			updated_at = excluded.updated_at
	`, username, url, nextSync, now); err != nil {
		return DeleteThreadResult{}, fmt.Errorf("upsert deleted thread: %w", err)
	}

	if currentSync.Valid {
		if _, err := tx.ExecContext(ctx, `
			UPDATE clients
			SET sync_number = ?, last_sync_at = ?
			WHERE username = ?
		`, nextSync, now, username); err != nil {
			return DeleteThreadResult{}, fmt.Errorf("update client sync: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return DeleteThreadResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return DeleteThreadResult{Deleted: true, Sync: nextSync}, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS clients (
			username TEXT PRIMARY KEY,
			client_id INTEGER NOT NULL,
			sync_number INTEGER NOT NULL,
			sync_day TEXT NOT NULL DEFAULT '',
			sync_count INTEGER NOT NULL DEFAULT 0,
			last_sync_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS threads (
			username TEXT NOT NULL,
			url TEXT NOT NULL,
			title TEXT NOT NULL,
			dir_name TEXT NOT NULL,
			read_value INTEGER NOT NULL,
			now_value INTEGER NOT NULL,
			count_value INTEGER NOT NULL,
			modified_sync INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (username, url)
		);

		CREATE TABLE IF NOT EXISTS deleted_threads (
			username TEXT NOT NULL,
			url TEXT NOT NULL,
			deleted_sync INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (username, url)
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}

	if err := ensureColumn(ctx, s.db, "clients", "sync_day", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, s.db, "clients", "sync_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, s.db, "threads", "modified_sync", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func loadOrCreateClient(ctx context.Context, tx *sql.Tx, username string, requestedClientID int64, requestedSyncNumber int64, dailyLimit int64, now time.Time) (ClientState, error) {
	var current clientRow
	row := tx.QueryRowContext(ctx, `
		SELECT client_id, sync_number, sync_day, sync_count
		FROM clients
		WHERE username = ?
	`, username)
	switch err := row.Scan(&current.ClientID, &current.SyncNumber, &current.SyncDay, &current.SyncCount); err {
	case nil:
	case sql.ErrNoRows:
		current.ClientID = requestedClientID
		if current.ClientID <= 0 {
			generatedID, genErr := randomPositiveInt64()
			if genErr != nil {
				return ClientState{}, fmt.Errorf("generate client_id: %w", genErr)
			}
			current.ClientID = generatedID
		}
	default:
		return ClientState{}, fmt.Errorf("load client: %w", err)
	}

	if requestedClientID > 0 {
		current.ClientID = requestedClientID
	}
	current.AccountType = "無料ユーザー"

	today := now.In(time.Local).Format(time.DateOnly)
	if current.SyncDay != today {
		current.SyncDay = today
		current.SyncCount = 0
	}
	if dailyLimit >= 0 {
		if current.SyncCount >= dailyLimit {
			current.Remain = 0
			return ClientState{ClientID: current.ClientID, SyncNumber: current.SyncNumber, Remain: current.Remain}, ErrSyncLimitExceeded
		}
		current.SyncCount++
		current.Remain = dailyLimit - current.SyncCount
	} else {
		current.Remain = -1
	}

	current.SyncNumber = maxInt64(current.SyncNumber, requestedSyncNumber) + 1

	_, err := tx.ExecContext(ctx, `
		INSERT INTO clients (username, client_id, sync_number, sync_day, sync_count, last_sync_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			client_id = excluded.client_id,
			sync_number = excluded.sync_number,
			sync_day = excluded.sync_day,
			sync_count = excluded.sync_count,
			last_sync_at = excluded.last_sync_at
	`, username, current.ClientID, current.SyncNumber, current.SyncDay, current.SyncCount, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ClientState{}, fmt.Errorf("upsert client: %w", err)
	}

	return current.ClientState, nil
}

func loadExistingThreads(ctx context.Context, tx *sql.Tx, username string) (map[string]ThreadRecord, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT url, title, dir_name, read_value, now_value, count_value, modified_sync
		FROM threads
		WHERE username = ?
	`, username)
	if err != nil {
		return nil, fmt.Errorf("query threads: %w", err)
	}
	defer rows.Close()

	result := map[string]ThreadRecord{}
	for rows.Next() {
		var record ThreadRecord
		if err := rows.Scan(&record.URL, &record.Title, &record.Dir, &record.Read, &record.Now, &record.Count, &record.Sync); err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		result[record.URL] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate threads: %w", err)
	}
	return result, nil
}

func loadDeletedThreads(ctx context.Context, tx *sql.Tx, username string) (map[string]DeletedThreadRecord, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT url, deleted_sync
		FROM deleted_threads
		WHERE username = ?
	`, username)
	if err != nil {
		return nil, fmt.Errorf("query deleted threads: %w", err)
	}
	defer rows.Close()

	result := map[string]DeletedThreadRecord{}
	for rows.Next() {
		var record DeletedThreadRecord
		if err := rows.Scan(&record.URL, &record.Sync); err != nil {
			return nil, fmt.Errorf("scan deleted thread: %w", err)
		}
		result[record.URL] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted threads: %w", err)
	}
	return result, nil
}

func splitIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

func ensureColumn(ctx context.Context, db *sql.DB, table string, column string, definition string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("query table info for %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan table info for %s: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table info for %s: %w", table, err)
	}

	if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func normalizeDirName(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "★"
	}
	return dir
}

func randomPositiveInt64() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	value := int64(binary.BigEndian.Uint64(buf[:]) & 0x7fffffffffffffff)
	if value == 0 {
		return 1, nil
	}
	return value, nil
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func mergeThreadRecord(prev ThreadRecord, current ThreadRecord) ThreadRecord {
	merged := current
	merged.Read = maxInt64(current.Read, prev.Read)
	merged.Now = maxInt64(current.Now, prev.Now)
	merged.Count = maxInt64(current.Count, prev.Count)
	if merged.Title == "" {
		merged.Title = prev.Title
	}
	if merged.Dir == "" {
		merged.Dir = prev.Dir
	}
	return merged
}

func threadRecordChanged(merged ThreadRecord, client ThreadRecord) bool {
	return merged.Title != client.Title ||
		merged.Dir != client.Dir ||
		merged.Read != client.Read ||
		merged.Now != client.Now ||
		merged.Count != client.Count
}

func threadPayloadChanged(left ThreadRecord, right ThreadRecord) bool {
	return left.Title != right.Title ||
		left.Dir != right.Dir ||
		left.Read != right.Read ||
		left.Now != right.Now ||
		left.Count != right.Count
}

func normalizeComparableRecord(record ThreadRecord) ThreadRecord {
	record.Dir = normalizeDirName(record.Dir)
	return record
}

func isDatabaseLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY")
}

func retryOnLocked[T any](ctx context.Context, op func() (T, error)) (T, error) {
	for i := 0; i < 15; i++ {
		res, err := op()
		if err == nil || !isDatabaseLockedError(err) {
			return res, err
		}
		select {
		case <-ctx.Done():
			var zero T
			return zero, fmt.Errorf("context canceled during retry: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return op()
}
