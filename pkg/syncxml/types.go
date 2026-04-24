package syncxml

import (
	"encoding/xml"
	"fmt"
	"log"
	"strings"
)

type Request struct {
	XMLName        xml.Name     `xml:"sync2ch_request"`
	SyncNumber     int64        `xml:"sync_number,attr"`
	ClientID       int64        `xml:"client_id,attr"`
	SyncRL         string       `xml:"sync_rl,attr"`
	ClientName     string       `xml:"client_name,attr"`
	ClientVer      string       `xml:"client_version,attr"`
	OS             string       `xml:"os,attr"`
	Entities       RequestItems `xml:"-"`
	ThreadGroup    RequestGroup `xml:"-"`
	HasEntities    bool         `xml:"-"`
	HasThreadGroup bool         `xml:"-"`
}

type rawRequest struct {
	XMLName      xml.Name       `xml:"sync2ch_request"`
	SyncNumber   int64          `xml:"sync_number,attr"`
	ClientID     int64          `xml:"client_id,attr"`
	SyncRL       string         `xml:"sync_rl,attr"`
	ClientName   string         `xml:"client_name,attr"`
	ClientVer    string         `xml:"client_version,attr"`
	OS           string         `xml:"os,attr"`
	Entities     RequestItems   `xml:"entities"`
	ThreadGroups []RequestGroup `xml:"thread_group"`
}

func (r *Request) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	var raw rawRequest
	if err := decoder.DecodeElement(&raw, &start); err != nil {
		return err
	}

	r.XMLName = raw.XMLName
	r.SyncNumber = raw.SyncNumber
	r.ClientID = raw.ClientID
	r.SyncRL = raw.SyncRL
	r.ClientName = raw.ClientName
	r.ClientVer = raw.ClientVer
	r.OS = raw.OS
	r.Entities = raw.Entities
	log.Println("Parsed request with entities:", raw.Entities, "and thread groups:", len(raw.ThreadGroups))
	r.HasEntities = len(raw.Entities.Threads) > 0
	r.ThreadGroup = selectFavoriteGroup(raw.ThreadGroups)
	r.HasThreadGroup = len(r.ThreadGroup.Dirs) > 0 || len(r.ThreadGroup.Threads) > 0 || len(r.ThreadGroup.THs) > 0

	if !r.HasEntities {
		r.Entities, r.ThreadGroup = normalizeFavoriteGroup(r.ThreadGroup)
		r.HasThreadGroup = r.HasThreadGroup || len(r.Entities.Threads) > 0
	}

	return nil
}

type RequestItems struct {
	Threads []RequestThread `xml:"th"`
}

type RequestThread struct {
	ID    string `xml:"id,attr"`
	URL   string `xml:"url,attr"`
	Title string `xml:"title,attr"`
	Read  int64  `xml:"read,attr"`
	Now   int64  `xml:"now,attr"`
	Count int64  `xml:"count,attr"`
}

type RequestGroup struct {
	Category string          `xml:"category,attr"`
	Struct   string          `xml:"struct,attr"`
	THs      []RequestThread `xml:"th"`
	Threads  []RequestThread `xml:"thread"`
	Dirs     []RequestDir    `xml:"dir"`
}

type RequestDir struct {
	Name    string          `xml:"name,attr"`
	IDList  string          `xml:"id_list,attr"`
	Dirs    []RequestDir    `xml:"dir"`
	Threads []RequestThread `xml:"thread"`
}

func selectFavoriteGroup(groups []RequestGroup) RequestGroup {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(group.Category), "favorite") {
			return group
		}
	}
	if len(groups) > 0 {
		return groups[0]
	}
	return RequestGroup{}
}

func normalizeFavoriteGroup(group RequestGroup) (RequestItems, RequestGroup) {
	if len(group.Dirs) == 0 && len(group.Threads) == 0 && len(group.THs) == 0 {
		return RequestItems{}, group
	}

	threads := make([]RequestThread, 0)
	dirIDs := make(map[string][]string)
	dirOrder := make([]string, 0)
	nextID := 0

	if len(group.Threads) > 0 {
		collectFavoriteThreads(RequestDir{Threads: group.Threads}, "", &threads, dirIDs, &dirOrder, &nextID)
	}

	if len(group.THs) > 0 {
		collectFavoriteThreads(RequestDir{Threads: group.THs}, "", &threads, dirIDs, &dirOrder, &nextID)
	}

	for _, dir := range group.Dirs {
		collectFavoriteThreads(dir, strings.TrimSpace(dir.Name), &threads, dirIDs, &dirOrder, &nextID)
	}

	normalizedDirs := make([]RequestDir, 0, len(dirOrder))
	for _, dirName := range dirOrder {
		normalizedDirs = append(normalizedDirs, RequestDir{
			Name:   dirName,
			IDList: strings.Join(dirIDs[dirName], ","),
		})
	}

	group.Dirs = normalizedDirs
	return RequestItems{Threads: threads}, group
}

func collectFavoriteThreads(dir RequestDir, currentDir string, threads *[]RequestThread, dirIDs map[string][]string, dirOrder *[]string, nextID *int) {
	effectiveDir := strings.TrimSpace(currentDir)
	if trimmedName := strings.TrimSpace(dir.Name); trimmedName != "" {
		effectiveDir = trimmedName
	}

	for _, thread := range dir.Threads {
		if strings.TrimSpace(thread.URL) == "" {
			continue
		}
		if strings.TrimSpace(thread.ID) == "" {
			thread.ID = fmt.Sprintf("%d", *nextID)
			*nextID++
		}
		*threads = append(*threads, thread)
		if _, ok := dirIDs[effectiveDir]; !ok {
			dirIDs[effectiveDir] = []string{}
			*dirOrder = append(*dirOrder, effectiveDir)
		}
		dirIDs[effectiveDir] = append(dirIDs[effectiveDir], thread.ID)
	}

	for _, child := range dir.Dirs {
		collectFavoriteThreads(child, effectiveDir, threads, dirIDs, dirOrder, nextID)
	}
}

type Response struct {
	XMLName     xml.Name        `xml:"sync2ch_response"`
	Result      string          `xml:"result,attr"`
	ClientID    int64           `xml:"client_id,attr"`
	SyncNumber  int64           `xml:"sync_number,attr"`
	Remain      int64           `xml:"remain,attr"`
	ThreadGroup []ResponseGroup `xml:"thread_group"`
	Entities    []ResponseItems `xml:"entities"`
}

type ResponseGroup struct {
	Category string              `xml:"category,attr,omitempty"`
	Struct   string              `xml:"struct,attr,omitempty"`
	Dirs     []ResponseDir       `xml:"dir,omitempty"`
	Threads  []ResponseThreadRef `xml:"th,omitempty"`
}

type ResponseDir struct {
	Name    string              `xml:"name,attr,omitempty"`
	Threads []ResponseThreadRef `xml:"th"`
}

type ResponseThreadRef struct {
	ID string `xml:"id,attr"`
}

type ResponseItems struct {
	Threads []ResponseThread `xml:"th"`
}

type ResponseThread struct {
	ID    string `xml:"id,attr"`
	URL   string `xml:"url,attr"`
	Title string `xml:"title,attr,omitempty"`
	Read  *int64 `xml:"read,attr,omitempty"`
	Now   *int64 `xml:"now,attr,omitempty"`
	Count *int64 `xml:"count,attr,omitempty"`
	State string `xml:"s,attr,omitempty"`
}

type LegacyResponse struct {
	XMLName     xml.Name            `xml:"sync2ch_response"`
	SyncNumber  int64               `xml:"sync_number,attr"`
	ThreadGroup []LegacyResponseDir `xml:"thread_group"`
}

type LegacyResponseDir struct {
	Category string                 `xml:"category,attr,omitempty"`
	Name     string                 `xml:"name,attr,omitempty"`
	Dirs     []LegacyResponseDir    `xml:"dir,omitempty"`
	Threads  []LegacyResponseThread `xml:"thread,omitempty"`
}

type LegacyResponseThread struct {
	URL   string `xml:"url,attr"`
	Title string `xml:"title,attr,omitempty"`
	Read  *int64 `xml:"read,attr,omitempty"`
	Now   *int64 `xml:"now,attr,omitempty"`
	Count *int64 `xml:"count,attr,omitempty"`
	State string `xml:"s,attr,omitempty"`
}
