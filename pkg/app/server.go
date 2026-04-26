package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/romtenma/monasync/pkg/config"
	"github.com/romtenma/monasync/pkg/store"
	"github.com/romtenma/monasync/pkg/syncxml"
)

type Server struct {
	cfg   config.Config
	store *store.Store
}

func New(cfg config.Config, store *store.Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleAdminPage)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/threads/delete", s.handleDeleteThread)
	mux.HandleFunc("/api/sync", s.handleSync)
	mux.HandleFunc("/api/sync3", s.handleSync)

	httpServer := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown error: %v", err)
		}
	}()

	log.Printf("listening on %s", s.cfg.Addr)
	err := httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.HealthCheck(r.Context()); err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	username, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	bodyReader, closeBody, err := decodeRequestBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	defer closeBody()
	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		http.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
		return
	}
	s.logXML("request", bodyBytes)

	var req syncxml.Request
	if err := xml.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid xml: %v", err), http.StatusBadRequest)
		return
	}

	records, clientState, err := s.store.ReplaceSnapshot(r.Context(), username, req, s.cfg.DailyLimit)
	if err != nil {
		if errors.Is(err, store.ErrSyncLimitExceeded) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("store error: %v", err), http.StatusInternalServerError)
		return
	}

	response := buildResponse(records, clientState)
	responseBytes, err := marshalResponse(r.URL.Path, response, records, clientState)
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal xml: %v", err), http.StatusInternalServerError)
		return
	}
	s.logXML("response", responseBytes)

	writer, closeWriter, err := encodeResponseWriter(w, r)
	if err != nil {
		http.Error(w, fmt.Sprintf("prepare response: %v", err), http.StatusInternalServerError)
		return
	}
	defer closeWriter()

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")
	w.WriteHeader(http.StatusOK)
	_, _ = writer.Write(responseBytes)
}

func buildResponse(records []store.ThreadRecord, state store.ClientState) syncxml.Response {
	type groupedThread struct {
		id     string
		record store.ThreadRecord
	}

	groups := map[string][]groupedThread{}
	entities := make([]syncxml.ResponseThread, 0, len(records))
	for index, record := range records {
		id := fmt.Sprintf("%d", index)
		groups[record.Dir] = append(groups[record.Dir], groupedThread{id: id, record: record})
		thread := syncxml.ResponseThread{
			ID:    id,
			URL:   record.URL,
			State: record.State,
		}
		if record.State == "a" || record.State == "u" {
			thread.Title = record.Title
			thread.Read = int64Ptr(record.Read)
			thread.Now = int64Ptr(record.Now)
			thread.Count = int64Ptr(record.Count)
		}
		entities = append(entities, thread)
	}

	dirNames := make([]string, 0, len(groups))
	for dirName := range groups {
		dirNames = append(dirNames, dirName)
	}
	sort.Strings(dirNames)

	dirs := make([]syncxml.ResponseDir, 0, len(dirNames))
	for _, dirName := range dirNames {
		threads := groups[dirName]
		sort.Slice(threads, func(i int, j int) bool {
			return strings.Compare(threads[i].record.URL, threads[j].record.URL) < 0
		})
		refs := make([]syncxml.ResponseThreadRef, 0, len(threads))
		for _, thread := range threads {
			refs = append(refs, syncxml.ResponseThreadRef{ID: thread.id})
		}
		dirs = append(dirs, syncxml.ResponseDir{Name: dirName, Threads: refs})
	}

	return syncxml.Response{
		Result:     "ok",
		ClientID:   state.ClientID,
		SyncNumber: state.SyncNumber,
		Remain:     state.Remain,
		Entities:   []syncxml.ResponseItems{{Threads: entities}},
		ThreadGroup: []syncxml.ResponseGroup{{
			Category: "favorite",
			Struct:   "default",
			Dirs:     dirs,
		}},
	}
}

func buildLegacyResponse(records []store.ThreadRecord, state store.ClientState) syncxml.LegacyResponse {
	groups := make(map[string][]store.ThreadRecord)
	for _, record := range records {
		groups[record.Dir] = append(groups[record.Dir], record)
	}

	dirNames := make([]string, 0, len(groups))
	for dirName := range groups {
		dirNames = append(dirNames, dirName)
	}
	sort.Strings(dirNames)

	dirs := make([]syncxml.LegacyResponseDir, 0, len(dirNames))
	for _, dirName := range dirNames {
		threads := groups[dirName]
		sort.Slice(threads, func(i int, j int) bool {
			return strings.Compare(threads[i].URL, threads[j].URL) < 0
		})

		items := make([]syncxml.LegacyResponseThread, 0, len(threads))
		for _, record := range threads {
			thread := syncxml.LegacyResponseThread{
				URL:   record.URL,
				State: record.State,
			}
			if record.State == "a" || record.State == "u" {
				thread.Title = record.Title
				thread.Read = int64Ptr(record.Read)
				thread.Now = int64Ptr(record.Now)
				thread.Count = int64Ptr(record.Count)
			}
			items = append(items, thread)
		}

		dirs = append(dirs, syncxml.LegacyResponseDir{
			Name:    dirName,
			Threads: items,
		})
	}

	return syncxml.LegacyResponse{
		SyncNumber: state.SyncNumber,
		ThreadGroup: []syncxml.LegacyResponseDir{{
			Category: "favorite",
			Dirs:     dirs,
		}},
	}
}

func marshalResponse(path string, response syncxml.Response, records []store.ThreadRecord, state store.ClientState) ([]byte, error) {
	if useLegacyResponse(path) {
		payload, err := xml.MarshalIndent(buildLegacyResponse(records, state), "", "  ")
		if err != nil {
			return nil, err
		}
		return append([]byte(xml.Header), rewriteEmptyElements(payload)...), nil
	}

	payload, err := xml.MarshalIndent(response, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), rewriteEmptyElements(payload)...), nil
}

func useLegacyResponse(path string) bool {
	return path == "/api/sync"
}

var emptyThElementPattern = regexp.MustCompile(`<th([^>]*)></th>`)
var emptyThreadElementPattern = regexp.MustCompile(`<thread([^>]*)></thread>`)
var responseElementOrderPattern = regexp.MustCompile(`(?s)(\s*)(<thread_group\b[^>]*>.*?</thread_group>)(\s*)(<entities\b[^>]*>.*?</entities>)`)

func rewriteEmptyElements(payload []byte) []byte {
	payload = emptyThElementPattern.ReplaceAll(payload, []byte(`<th$1 />`))
	payload = emptyThreadElementPattern.ReplaceAll(payload, []byte(`<thread$1 />`))
	return responseElementOrderPattern.ReplaceAll(payload, []byte(`$1$4$3$2`))
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func int64Ptr(value int64) *int64 {
	return &value
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	username, password, ok := r.BasicAuth()
	if !ok || username != s.cfg.User || password != s.cfg.Password {
		log.Printf("authenticate: failed for user %q from %s", username, r.RemoteAddr)
		w.Header().Set("WWW-Authenticate", `Basic realm="MonaSync"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return "", false
	}
	return username, true
}

func (s *Server) logXML(direction string, payload []byte) {
	if !s.cfg.DumpXML {
		return
	}
	log.Printf("sync %s xml:\n%s", direction, string(payload))
}

func decodeRequestBody(r *http.Request) (io.Reader, func(), error) {
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	if encoding == "" {
		encoding = strings.ToLower(strings.TrimSpace(r.Header.Get("Encoding")))
	}
	if !strings.Contains(encoding, "gzip") {
		return r.Body, func() {
			_ = r.Body.Close()
		}, nil
	}

	gzipReader, err := gzip.NewReader(r.Body)
	if err != nil {
		return nil, nil, err
	}
	return gzipReader, func() {
		_ = gzipReader.Close()
		_ = r.Body.Close()
	}, nil
}

func encodeResponseWriter(w http.ResponseWriter, r *http.Request) (io.Writer, func(), error) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
		return w, func() {}, nil
	}

	w.Header().Set("Content-Encoding", "gzip")
	gzipWriter := gzip.NewWriter(w)
	return gzipWriter, func() {
		_ = gzipWriter.Close()
	}, nil
}
