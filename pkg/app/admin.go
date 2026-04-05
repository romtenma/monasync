package app

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/romtenma/monasync/pkg/store"
)

type adminPageData struct {
	Username string
	Threads  []store.ThreadRecord
	Message  string
}

var adminPageTmpl = template.Must(template.New("admin-page").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MonaSync</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6efe3;
      --paper: rgba(255, 251, 245, 0.92);
      --ink: #1d1a17;
      --muted: #6b6258;
      --line: rgba(77, 57, 36, 0.14);
      --accent: #0f766e;
      --accent-strong: #115e59;
      --danger: #b42318;
      --danger-bg: #fff1ef;
      --shadow: 0 24px 60px rgba(69, 44, 23, 0.12);
    }

    * { box-sizing: border-box; }

    body {
      margin: 0;
      min-height: 100vh;
      font-family: "Bahnschrift", "Yu Gothic UI", "Segoe UI Variable Text", sans-serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, rgba(15, 118, 110, 0.18), transparent 26%),
        radial-gradient(circle at bottom right, rgba(180, 35, 24, 0.14), transparent 22%),
        linear-gradient(160deg, #fbf6ee 0%, #f3e6d6 52%, #efe3d7 100%);
    }

    .shell {
      width: min(1120px, calc(100% - 32px));
      margin: 32px auto;
      padding: 28px;
      border: 1px solid rgba(255, 255, 255, 0.45);
      border-radius: 24px;
      background: var(--paper);
      box-shadow: var(--shadow);
      backdrop-filter: blur(18px);
    }

    .hero {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: end;
      margin-bottom: 20px;
    }

    h1 {
      margin: 0;
      font-size: clamp(2rem, 4vw, 3.5rem);
      line-height: 0.95;
      letter-spacing: -0.05em;
    }

    .sub {
      margin: 10px 0 0;
      color: var(--muted);
      font-size: 0.98rem;
    }

    .badge {
      padding: 10px 14px;
      border-radius: 999px;
      background: rgba(15, 118, 110, 0.1);
      color: var(--accent-strong);
      font-weight: 700;
      white-space: nowrap;
    }

    .notice {
      margin: 0 0 20px;
      padding: 14px 16px;
      border-left: 4px solid var(--accent);
      border-radius: 14px;
      background: rgba(15, 118, 110, 0.08);
    }

    .table-wrap {
      overflow-x: auto;
      border: 1px solid var(--line);
      border-radius: 18px;
      background: rgba(255, 255, 255, 0.72);
    }

    table {
      width: 100%;
      border-collapse: collapse;
      min-width: 720px;
    }

    th, td {
      padding: 14px 16px;
      text-align: left;
      border-bottom: 1px solid var(--line);
      vertical-align: top;
    }

    th {
      font-size: 0.82rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--muted);
      background: rgba(255, 250, 244, 0.88);
    }

    tr:last-child td {
      border-bottom: 0;
    }

    .title {
      font-weight: 700;
      margin-bottom: 6px;
    }

    .url {
      color: var(--muted);
      word-break: break-all;
      font-size: 0.92rem;
    }

    .dir {
      font-weight: 700;
      color: var(--accent-strong);
    }

    .metrics {
      font-feature-settings: "tnum" 1;
      white-space: nowrap;
    }

    .delete-form {
      margin: 0;
    }

    .delete-button {
      border: 0;
      border-radius: 999px;
      padding: 10px 14px;
      background: var(--danger-bg);
      color: var(--danger);
      font: inherit;
      font-weight: 700;
      cursor: pointer;
    }

    .delete-button:hover {
      background: #ffe3de;
    }

    .empty {
      padding: 40px 24px;
      text-align: center;
      color: var(--muted);
    }

    @media (max-width: 760px) {
      .shell {
        width: min(100% - 16px, 100%);
        margin: 8px auto;
        padding: 20px;
        border-radius: 20px;
      }

      .hero {
        flex-direction: column;
        align-items: flex-start;
      }
    }
  </style>
</head>
<body>
  <main class="shell">
    <section class="hero">
      <div>
        <h1>MonaSync （　´∀｀）</h1>
        <p class="sub">Review the current snapshot and remove entries directly from the browser.</p>
      </div>
      <div class="badge">User: {{.Username}} / {{len .Threads}} items</div>
    </section>
    {{if .Message}}<p class="notice">{{.Message}}</p>{{end}}
    <section class="table-wrap">
      {{if .Threads}}
      <table>
        <thead>
          <tr>
            <th>Thread</th>
            <th>Folder</th>
            <th>Read</th>
            <th>Now</th>
            <th>Count</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {{range .Threads}}
          <tr>
            <td>
              <div class="title">{{.Title}}</div>
              <div class="url">{{.URL}}</div>
            </td>
            <td class="dir">{{.Dir}}</td>
            <td class="metrics">{{.Read}}</td>
            <td class="metrics">{{.Now}}</td>
            <td class="metrics">{{.Count}}</td>
            <td>
              <form class="delete-form" method="post" action="/threads/delete">
                <input type="hidden" name="url" value="{{.URL}}">
                <button class="delete-button" type="submit">Delete</button>
              </form>
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <div class="empty">No stored threads yet.</div>
      {{end}}
    </section>
  </main>
</body>
</html>`))

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	username, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	threads, err := s.store.ListThreads(r.Context(), username)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		log.Printf("list threads: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminPageTmpl.Execute(w, adminPageData{
		Username: username,
		Threads:  threads,
		Message:  strings.TrimSpace(r.URL.Query().Get("message")),
	}); err != nil {
		log.Printf("render admin page: %v", err)
	}
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	username, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	threadURL := strings.TrimSpace(r.FormValue("url"))
	if threadURL == "" {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	result, err := s.store.DeleteThread(r.Context(), username, threadURL)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		log.Printf("delete thread: %v", err)
		return
	}

	message := "Thread was not found."
	if result.Deleted {
		message = "Thread deleted."
	}
	http.Redirect(w, r, "/?message="+url.QueryEscape(message), http.StatusSeeOther)
}
